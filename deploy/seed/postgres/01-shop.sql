-- Two copies of a small shop schema with deliberate differences, used to try
-- the compare tool. Connect with user compare_ro / compare_ro, schema public.

CREATE ROLE compare_ro LOGIN PASSWORD 'compare_ro';
CREATE DATABASE shop_source;
CREATE DATABASE shop_target;

-- ---------------------------------------------------------------- source
\connect shop_source

CREATE TABLE customers (
    id         integer PRIMARY KEY,
    name       varchar(100) NOT NULL,
    email      varchar(150) NOT NULL UNIQUE,
    city       varchar(80),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz
);

CREATE TABLE products (
    id    integer PRIMARY KEY,
    sku   varchar(20) NOT NULL UNIQUE,
    name  varchar(100) NOT NULL,
    price numeric(10, 2) NOT NULL CHECK (price >= 0),
    stock integer NOT NULL DEFAULT 0
);

CREATE TABLE orders (
    id          integer PRIMARY KEY,
    customer_id integer NOT NULL REFERENCES customers (id),
    status      text NOT NULL,
    total       numeric(12, 2) NOT NULL,
    ordered_at  timestamp NOT NULL
);
CREATE INDEX idx_orders_ordered_at ON orders (ordered_at);

CREATE TABLE order_items (
    order_id   integer NOT NULL,
    line_no    integer NOT NULL,
    product_id integer NOT NULL,
    qty        integer NOT NULL,
    price      numeric(10, 2) NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

CREATE TABLE event_log (
    event_time timestamp NOT NULL,
    source     text NOT NULL,
    message    text NOT NULL,
    payload    jsonb
);

CREATE TABLE legacy_notes (
    id   integer PRIMARY KEY,
    note text
);

CREATE TABLE big_ledger (
    id      bigint PRIMARY KEY,
    account varchar(20) NOT NULL,
    amount  numeric(12, 2) NOT NULL,
    note    text
);

CREATE VIEW v_customer_orders AS
SELECT c.id AS customer_id, c.name, count(o.id) AS orders, coalesce(sum(o.total), 0) AS total
FROM customers c LEFT JOIN orders o ON o.customer_id = c.id
GROUP BY c.id, c.name;

CREATE FUNCTION order_total(p_order integer) RETURNS numeric
LANGUAGE sql STABLE AS $$
    SELECT coalesce(sum(qty * price), 0) FROM order_items WHERE order_id = p_order
$$;

INSERT INTO customers (id, name, email, city, created_at) VALUES
    (1, 'Andi Wijaya', 'andi@example.com', 'Jakarta', '2024-01-05 08:00:00+07'),
    (2, 'Budi Santoso', 'budi@example.com', 'Bandung', '2024-01-06 09:30:00+07'),
    (3, 'Citra Lestari', 'citra@example.com', 'Surabaya', '2024-02-01 10:00:00+07'),
    (4, 'Dewi Anggraini', 'dewi@example.com', NULL, '2024-02-10 11:15:00+07'),
    (5, 'Eko Prasetyo', 'eko@example.com', 'Medan', '2024-03-03 12:00:00+07'),
    (6, 'Fitri Handayani', 'fitri@example.com', 'Makassar', '2024-03-15 13:45:00+07'),
    (7, 'Gita Permata', 'gita@example.com', 'Semarang', '2024-04-01 14:00:00+07'),
    (8, 'Hadi Kurniawan', 'hadi@example.com', 'Yogyakarta', '2024-04-20 15:30:00+07'),
    (9, 'Indah Sari', 'indah@example.com', 'Denpasar', '2024-05-05 16:00:00+07'),
    (10, 'Joko Susilo', 'joko@example.com', 'Malang', '2024-05-25 17:10:00+07');

INSERT INTO products VALUES
    (1, 'SKU-001', 'Kopi Arabika 250g', 85000.00, 120),
    (2, 'SKU-002', 'Teh Hijau 100g', 45000.00, 80),
    (3, 'SKU-003', 'Gula Aren 500g', 30000.00, 200),
    (4, 'SKU-004', 'Madu Hutan 350ml', 120000.00, 40);

INSERT INTO orders VALUES
    (1, 1, 'paid', 170000.00, '2024-06-01 10:00:00'),
    (2, 2, 'shipped', 45000.00, '2024-06-02 11:00:00'),
    (3, 3, 'new', 150000.00, '2024-06-03 12:00:00'),
    (4, 1, 'cancelled', 30000.00, '2024-06-04 13:00:00');

INSERT INTO order_items VALUES
    (1, 1, 1, 2, 85000.00),
    (2, 1, 2, 1, 45000.00),
    (3, 1, 3, 1, 30000.00),
    (3, 2, 4, 1, 120000.00),
    (4, 1, 3, 1, 30000.00);

INSERT INTO event_log VALUES
    ('2024-06-01 10:00:00', 'checkout', 'order 1 paid', '{"order": 1}'),
    ('2024-06-02 11:00:00', 'shipping', 'order 2 shipped', '{"order": 2}'),
    ('2024-06-03 12:00:00', 'checkout', 'order 3 created', NULL);

INSERT INTO legacy_notes VALUES (1, 'migrated from the old system');

INSERT INTO big_ledger (id, account, amount, note)
SELECT n, 'ACC-' || lpad((n % 1000)::text, 4, '0'), round(((n * 137) % 1000000) / 100.0, 2), 'entry ' || n
FROM generate_series(1, 250000) AS n;

GRANT USAGE ON SCHEMA public TO compare_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO compare_ro;

-- ---------------------------------------------------------------- target
\connect shop_target

-- city is longer and phone is new.
CREATE TABLE customers (
    id         integer PRIMARY KEY,
    name       varchar(100) NOT NULL,
    email      varchar(150) NOT NULL UNIQUE,
    city       varchar(100),
    phone      varchar(20),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz
);

-- The price check constraint is missing.
CREATE TABLE products (
    id    integer PRIMARY KEY,
    sku   varchar(20) NOT NULL UNIQUE,
    name  varchar(100) NOT NULL,
    price numeric(10, 2) NOT NULL,
    stock integer NOT NULL DEFAULT 0
);

-- The ordered_at index is missing.
CREATE TABLE orders (
    id          integer PRIMARY KEY,
    customer_id integer NOT NULL REFERENCES customers (id),
    status      text NOT NULL,
    total       numeric(12, 2) NOT NULL,
    ordered_at  timestamp NOT NULL
);

CREATE TABLE order_items (
    order_id   integer NOT NULL,
    line_no    integer NOT NULL,
    product_id integer NOT NULL,
    qty        integer NOT NULL,
    price      numeric(10, 2) NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

CREATE TABLE event_log (
    event_time timestamp NOT NULL,
    source     text NOT NULL,
    message    text NOT NULL,
    payload    jsonb
);

CREATE TABLE big_ledger (
    id      bigint PRIMARY KEY,
    account varchar(20) NOT NULL,
    amount  numeric(12, 2) NOT NULL,
    note    text
);

CREATE TABLE promo_codes (
    code       varchar(20) PRIMARY KEY,
    percent    integer NOT NULL,
    expires_at date
);

CREATE VIEW v_customer_orders AS
SELECT c.id AS customer_id, c.name, count(o.id) AS orders, coalesce(sum(o.total), 0) AS total
FROM customers c LEFT JOIN orders o ON o.customer_id = c.id AND o.status <> 'cancelled'
GROUP BY c.id, c.name;

-- Cancelled orders count as zero here.
CREATE FUNCTION order_total(p_order integer) RETURNS numeric
LANGUAGE sql STABLE AS $$
    SELECT coalesce(sum(i.qty * i.price), 0)
    FROM order_items i JOIN orders o ON o.id = i.order_id
    WHERE i.order_id = p_order AND o.status <> 'cancelled'
$$;

INSERT INTO customers (id, name, email, city, created_at) VALUES
    (1, 'Andi Wijaya', 'andi@example.com', 'Jakarta', '2024-01-05 08:00:00+07'),
    (2, 'Budi Santoso', 'budi.santoso@example.com', 'Bandung', '2024-01-06 09:30:00+07'),
    (3, 'Citra L.', 'citra@example.com', 'Surabaya Timur', '2024-02-01 10:00:00+07'),
    (4, 'Dewi Anggraini', 'dewi@example.com', 'Bogor', '2024-02-10 11:15:00+07'),
    (6, 'Fitri Handayani', 'fitri@example.com', 'Makassar', '2024-03-15 13:45:00+07'),
    (7, 'Gita Permata', 'gita@example.com', 'Semarang', '2024-04-01 14:00:00+07'),
    (8, 'Hadi Kurniawan', 'hadi@example.com', 'Yogyakarta', '2024-04-20 15:30:00+07'),
    (9, 'Indah Sari', 'indah@example.com', 'Denpasar', '2024-05-05 16:00:00+07'),
    (10, 'Joko Susilo', 'joko@example.com', 'Malang', '2024-05-25 17:10:00+07'),
    (11, 'Kartika Putri', 'kartika@example.com', 'Padang', '2024-06-10 08:00:00+07');

INSERT INTO products VALUES
    (1, 'SKU-001', 'Kopi Arabika 250g', 85000.00, 120),
    (2, 'SKU-002', 'Teh Hijau 100g', 45000.00, 80),
    (3, 'SKU-003', 'Gula Aren 500g', 30000.00, 200),
    (4, 'SKU-004', 'Madu Hutan 350ml', 120000.00, 40);

INSERT INTO orders VALUES
    (1, 1, 'paid', 170000.00, '2024-06-01 10:00:00'),
    (2, 2, 'shipped', 45000.00, '2024-06-02 11:00:00'),
    (3, 3, 'paid', 150000.00, '2024-06-03 12:00:00'),
    (4, 1, 'cancelled', 30000.00, '2024-06-04 13:00:00');

INSERT INTO order_items VALUES
    (1, 1, 1, 3, 85000.00),
    (2, 1, 2, 1, 45000.00),
    (3, 1, 3, 1, 30000.00),
    (3, 2, 4, 1, 120000.00),
    (4, 1, 3, 1, 30000.00);

INSERT INTO event_log VALUES
    ('2024-06-01 10:00:00', 'checkout', 'order 1 paid', '{"order": 1}'),
    ('2024-06-02 11:00:00', 'shipping', 'order 2 delivered', '{"order": 2}'),
    ('2024-06-03 12:00:00', 'checkout', 'order 3 created', NULL);

INSERT INTO promo_codes VALUES ('HEMAT10', 10, '2024-12-31');

INSERT INTO big_ledger (id, account, amount, note)
SELECT n, 'ACC-' || lpad((n % 1000)::text, 4, '0'), round(((n * 137) % 1000000) / 100.0, 2), 'entry ' || n
FROM generate_series(1, 250000) AS n
WHERE n NOT IN (500, 180000);
UPDATE big_ledger SET amount = amount + 1 WHERE id IN (1234, 98765, 200001);
UPDATE big_ledger SET note = NULL WHERE id = 150000;
INSERT INTO big_ledger VALUES (250001, 'ACC-0001', 10.00, 'late entry');

GRANT USAGE ON SCHEMA public TO compare_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO compare_ro;
