# DB Compare

Tool web internal untuk membandingkan **struktur** dan **data** dua database dengan engine yang sama (MySQL ↔ MySQL, PostgreSQL ↔ PostgreSQL). Alurnya: ringkasan global → detail per tabel → detail per baris, lalu hasilnya bisa diekspor ke Excel.

Rencana desainnya ada di [`../db-compare-tool-plan.md`](../db-compare-tool-plan.md).

## Struktur

```
api/     Go HTTP server (chi, pgx, go-sql-driver/mysql, excelize)
  cmd/server          entrypoint
  internal/engine     compare engine: inspeksi schema, schema diff, checksum, row diff
  internal/runner     menjalankan run di dalam request, progres, pool koneksi
  internal/store      App DB (PostgreSQL) + migrasi
  internal/api        REST + Server-Sent Events
  internal/export     workbook Excel (streaming)
web/     Next.js (App Router) + TanStack Query + Tailwind
deploy/  nginx.conf dan data contoh MySQL/PostgreSQL
```

## Cara kerja singkat

1. **Compare now** di halaman project membuat run berstatus `pending`, lalu halaman run memanggil `POST /api/runs/{id}/execute`. Request itu tetap terbuka sebagai stream SSE selama run berjalan.
2. **Struktur:** metadata kedua DB dibaca paralel dan dibandingkan (tabel, kolom, PK, index, constraint, view, function/procedure).
3. **Data per tabel:** `COUNT(*)` dan checksum dihitung **di dalam database** (hash MD5 per baris, dijumlahkan sehingga tidak bergantung urutan), jadi data tidak ditarik ke server.
4. **Detail baris**, dijalankan saat run atau per tabel dari halaman detail:
   - Tabel yang lebih besar dari *chunk size* dipecah per rentang primary key.
   - Chunk yang checksum-nya sama dilewati.
   - Chunk yang berbeda dibaca terurut dari kedua sisi, lalu di-*merge-join* menjadi *different / only in source / only in target*.
5. **Penyimpanan hasil:** setiap hasil langsung disimpan ke App DB.
   - Jika tab ditutup, run berhenti dengan status `cancelled`. Tabel yang sudah selesai tetap tersimpan, dan tombol **Resume** melanjutkan sisanya.
   - Jika server mati, run ditandai `interrupted` saat server start berikutnya.
6. **Export Excel:** dibuat dari hasil yang tersimpan, tanpa query ulang ke DB sumber. Isinya sheet Summary, Schema, Data, dan satu sheet per tabel yang punya perbedaan baris.

## Menjalankan dengan Docker Compose

```bash
cp .env.example .env
# isi APP_SECRET_KEY: openssl rand -base64 32
docker compose --profile samples up --build
```

Buka http://localhost:8088 lalu login sebagai admin (lihat [Login dan role](#login-dan-role)).

Profile `samples` ikut menjalankan dua DB contoh yang sengaja berbeda. Tambahkan koneksi berikut di halaman **Connections**. Host-nya memakai nama service karena API berjalan di jaringan Docker yang sama.

| Nama | Engine | Host | Port | Database | Schema | User / Password |
|---|---|---|---|---|---|---|
| mysql-source | MySQL | sample-mysql | 3306 | shop_source | – | compare_ro / compare_ro |
| mysql-target | MySQL | sample-mysql | 3306 | shop_target | – | compare_ro / compare_ro |
| pg-source | PostgreSQL | sample-postgres | 5432 | shop_source | public | compare_ro / compare_ro |
| pg-target | PostgreSQL | sample-postgres | 5432 | shop_target | public | compare_ro / compare_ro |

Setelah itu, buat project `mysql-source → mysql-target`. Isi *chunk size* dengan 50000 agar tabel `big_ledger` (250 ribu baris) ikut diuji per chunk.

Perbedaan yang sudah disiapkan di data contoh:
- **Kolom `customers`:** `city` berubah panjang, dan ada kolom baru `phone`.
- **Index:** index `orders.ordered_at` hilang di target.
- **Tabel:** `legacy_notes` hanya ada di source, `promo_codes` hanya ada di target.
- **View dan function:** view `v_customer_orders` berbeda, dan function `order_total` berbeda (khusus PostgreSQL).
- **Baris berbeda:** ada di `customers`, `orders`, `order_items`, dan `big_ledger`.
- **Tabel tanpa primary key:** `event_log` perlu dipilihkan key secara manual, misalnya `event_time, source`.

## Menjalankan untuk development

Prasyarat: Go ≥ 1.25, Node ≥ 20, pnpm, dan PostgreSQL untuk App DB.

Setiap bagian punya file `.env` sendiri (salin dari `.env.example` jika belum ada):

| File | Dibaca oleh | Isi utama |
|---|---|---|
| `api/.env` | API saat start, dari working directory (`ENV_FILE` untuk path lain) | `APP_DATABASE_URL`, `APP_SECRET_KEY`, admin awal, batas-batas |
| `web/.env` | Next.js (`pnpm dev` / `pnpm build`) | `API_ORIGIN` |
| `.env` (root) | Docker Compose | `APP_SECRET_KEY` dan pengaturan container `api` |

Variabel yang sudah di-set di shell selalu mengalahkan isi `api/.env`. `APP_SECRET_KEY` harus tetap sama; jika berubah, password koneksi yang tersimpan tidak bisa dibuka.

```bash
# API
cd api
go run ./cmd/server

# Web (terminal lain)
cd web
pnpm install
pnpm dev            # /api di-rewrite ke API_ORIGIN dari web/.env
```

Verifikasi statis:

```bash
cd api && go build ./... && go vet ./...
cd web && pnpm build
```

## Konfigurasi API

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_DATABASE_URL` | – (wajib) | PostgreSQL untuk App DB. Migrasi dijalankan otomatis saat start. |
| `APP_SECRET_KEY` | – (wajib) | 32 byte base64 untuk enkripsi password koneksi (AES-256-GCM). Jika diganti, password lama tidak bisa dibuka. |
| `LISTEN_ADDR` | `:8080` | |
| `ADMIN_USERNAME` | `admin` | Username admin pertama. Hanya dipakai saat tabel `users` masih kosong. |
| `ADMIN_PASSWORD` | – | Password admin pertama (8–72 karakter). Jika kosong, password dibuat acak dan dicetak **sekali** di log API. |
| `SESSION_TTL` | `12h` | Lama sesi login berlaku. |
| `RETENTION_DAYS` | `30` | Run yang lebih tua dari ini dihapus otomatis. Isi `0` untuk menonaktifkan. |
| `MAX_PARALLEL_TABLES` | `8` | Batas atas tabel paralel per run. |
| `MAX_CONNS_PER_DB` | `10` | Batas koneksi server ini ke **setiap** DB yang dibandingkan, dihitung untuk semua run sekaligus. |
| `QUERY_TIMEOUT` | `30m` | Batas waktu per query checksum/baca chunk. |
| `HEARTBEAT_INTERVAL` | `10s` | Interval heartbeat SSE dan status run. |

### Login dan role

Login memakai **user lokal** (username + password, di-hash dengan bcrypt). Sesi disimpan di tabel `sessions` dan dikirim sebagai cookie `HttpOnly`.

- **Admin pertama:** saat start pertama (tabel `users` kosong), API membuat admin dari `ADMIN_USERNAME` / `ADMIN_PASSWORD`.
  - Jika `ADMIN_PASSWORD` kosong, cari baris log `created initial admin with a generated password`.
  - Segera ganti password lewat menu **Account**.
- **User lain:** admin menambahkan user dari menu **Users**. Dari menu yang sama, admin bisa mengganti role, menonaktifkan user, reset password, atau menghapus user.
  - Admin tidak bisa mengubah akunnya sendiri dari menu Users.
  - Minimal satu admin aktif harus tetap ada.
- **Gagal login:** setelah 5 kali gagal untuk username + IP yang sama, login ditolak selama 1 menit.
- **Sesi berakhir** saat user dinonaktifkan atau password-nya di-reset.

| Aksi | Viewer | Operator | Admin |
|---|:-:|:-:|:-:|
| Lihat project, koneksi, run, dan hasil | ✓ | ✓ | ✓ |
| Export Excel | ✓ | ✓ | ✓ |
| Jalankan / resume / stop compare, *Compare rows* | – | ✓ | ✓ |
| Buat dan edit project | – | ✓ | ✓ |
| Hapus project dan run | – | – | ✓ |
| Kelola koneksi (tambah, edit, test, hapus) | – | – | ✓ |
| Kelola user dan role | – | – | ✓ |
| Compare yang memakai koneksi **Protected** | – | – | ✓ |

Koneksi bisa ditandai **Protected** (misalnya production). Compare yang memakai koneksi itu, sebagai source atau target, hanya bisa dijalankan admin. Hasilnya tetap bisa dilihat semua role.

Semua aturan di atas dicek di API. UI hanya menyembunyikan tombol yang tidak boleh dipakai.

### Keamanan

- **User DB:** gunakan user **read-only**. Sesi PostgreSQL dibuka dengan `default_transaction_read_only=on`. Tombol *Test connection* memberi peringatan jika user punya hak tulis.
- **HTTPS:** jalankan di belakang HTTPS di luar lingkungan lokal. Cookie sesi otomatis diberi flag `Secure` jika request datang lewat TLS atau `X-Forwarded-Proto: https`.
- **Request lintas origin:** request yang mengubah data dan datang dari origin lain ditolak (`http.CrossOriginProtection`).
- **Data sensitif:** hasil compare dan file Excel berisi data asli. Gunakan opsi *Mask columns* untuk kolom sensitif, dan atur `RETENTION_DAYS`.
- **Audit:** semua aksi dicatat di tabel `audit_logs`: koneksi, project, run, cancel, export, delete, login/logout (termasuk yang gagal), dan perubahan user.

## Keterbatasan saat ini

- **Satu instance:** server diasumsikan berjalan sebagai satu instance. Status "sedang berjalan" disimpan di memori proses.
- **Run terikat ke tab:** run berhenti jika tab ditutup. Hasil parsial tetap tersimpan, dan run bisa di-resume.
- **Konsistensi data:** tidak ada snapshot lintas query. Data yang berubah selama compare bisa ikut terbaca berbeda, jadi hasil dianggap mewakili waktu pemindaian.
- **Versi DB:** PostgreSQL minimal 12. MySQL 5.7 dan 8.x didukung. MariaDB belum diuji.
- **Objek yang belum dibandingkan:** trigger, sequence, CHECK constraint di MySQL, dan komentar kolom.
- **Login:** hanya user lokal, belum ada SSO/LDAP. Belum ada halaman untuk melihat audit log.
- **Sync:** belum ada generator script sync. Fitur ini masuk fase berikutnya di rencana.
- **Dependency Go:** `go.mod`/`go.sum` disusun dari cache module lokal karena `proxy.golang.org` tidak bisa diakses dari mesin pengembangan. Jalankan `go mod tidy` sekali di lingkungan yang punya akses jaringan.
