-- Two copies of a small shop schema with deliberate differences, used to try
-- the compare tool. Connect with user compare_ro / compare_ro.

SET SESSION cte_max_recursion_depth = 1000000;

CREATE DATABASE shop_source;
CREATE DATABASE shop_target;

CREATE USER 'compare_ro'@'%' IDENTIFIED BY 'compare_ro';
GRANT SELECT, SHOW VIEW ON shop_source.* TO 'compare_ro'@'%';
GRANT SELECT, SHOW VIEW ON shop_target.* TO 'compare_ro'@'%';

-- ---------------------------------------------------------------- source
USE shop_source;

CREATE TABLE customers (
    id         INT PRIMARY KEY,
    name       VARCHAR(100) NOT NULL,
    email      VARCHAR(150) NOT NULL,
    city       VARCHAR(80),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NULL,
    UNIQUE KEY uq_customers_email (email)
);

CREATE TABLE products (
    id    INT PRIMARY KEY,
    sku   VARCHAR(20) NOT NULL UNIQUE,
    name  VARCHAR(100) NOT NULL,
    price DECIMAL(10, 2) NOT NULL,
    stock INT NOT NULL DEFAULT 0
);

CREATE TABLE orders (
    id          INT PRIMARY KEY,
    customer_id INT NOT NULL,
    status      ENUM('new', 'paid', 'shipped', 'cancelled') NOT NULL,
    total       DECIMAL(12, 2) NOT NULL,
    ordered_at  DATETIME NOT NULL,
    KEY idx_orders_ordered_at (ordered_at),
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
);

CREATE TABLE order_items (
    order_id   INT NOT NULL,
    line_no    INT NOT NULL,
    product_id INT NOT NULL,
    qty        INT NOT NULL,
    price      DECIMAL(10, 2) NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

CREATE TABLE event_log (
    event_time DATETIME NOT NULL,
    source     VARCHAR(30) NOT NULL,
    message    VARCHAR(255) NOT NULL
);

CREATE TABLE legacy_notes (
    id   INT PRIMARY KEY,
    note TEXT
);

CREATE TABLE big_ledger (
    id      BIGINT PRIMARY KEY,
    account VARCHAR(20) NOT NULL,
    amount  DECIMAL(12, 2) NOT NULL,
    note    VARCHAR(100)
);

CREATE VIEW v_customer_orders AS
SELECT c.id AS customer_id, c.name, COUNT(o.id) AS orders, COALESCE(SUM(o.total), 0) AS total
FROM customers c LEFT JOIN orders o ON o.customer_id = c.id
GROUP BY c.id, c.name;

INSERT INTO customers (id, name, email, city, created_at) VALUES
    (1, 'Andi Wijaya', 'andi@example.com', 'Jakarta', '2024-01-05 08:00:00'),
    (2, 'Budi Santoso', 'budi@example.com', 'Bandung', '2024-01-06 09:30:00'),
    (3, 'Citra Lestari', 'citra@example.com', 'Surabaya', '2024-02-01 10:00:00'),
    (4, 'Dewi Anggraini', 'dewi@example.com', NULL, '2024-02-10 11:15:00'),
    (5, 'Eko Prasetyo', 'eko@example.com', 'Medan', '2024-03-03 12:00:00'),
    (6, 'Fitri Handayani', 'fitri@example.com', 'Makassar', '2024-03-15 13:45:00'),
    (7, 'Gita Permata', 'gita@example.com', 'Semarang', '2024-04-01 14:00:00'),
    (8, 'Hadi Kurniawan', 'hadi@example.com', 'Yogyakarta', '2024-04-20 15:30:00'),
    (9, 'Indah Sari', 'indah@example.com', 'Denpasar', '2024-05-05 16:00:00'),
    (10, 'Joko Susilo', 'joko@example.com', 'Malang', '2024-05-25 17:10:00');

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
    ('2024-06-01 10:00:00', 'checkout', 'order 1 paid'),
    ('2024-06-02 11:00:00', 'shipping', 'order 2 shipped'),
    ('2024-06-03 12:00:00', 'checkout', 'order 3 created');

INSERT INTO legacy_notes VALUES (1, 'migrated from the old system');

INSERT INTO big_ledger (id, account, amount, note)
WITH RECURSIVE seq (n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 250000)
SELECT n, CONCAT('ACC-', LPAD(n % 1000, 4, '0')), ROUND(MOD(n * 137, 1000000) / 100, 2), CONCAT('entry ', n)
FROM seq;

-- ---------------------------------------------------------------- target
USE shop_target;

-- city is longer and phone is new.
CREATE TABLE customers (
    id         INT PRIMARY KEY,
    name       VARCHAR(100) NOT NULL,
    email      VARCHAR(150) NOT NULL,
    city       VARCHAR(100),
    phone      VARCHAR(20),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NULL,
    UNIQUE KEY uq_customers_email (email)
);

CREATE TABLE products LIKE shop_source.products;

-- The ordered_at index is missing.
CREATE TABLE orders (
    id          INT PRIMARY KEY,
    customer_id INT NOT NULL,
    status      ENUM('new', 'paid', 'shipped', 'cancelled') NOT NULL,
    total       DECIMAL(12, 2) NOT NULL,
    ordered_at  DATETIME NOT NULL,
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
);

CREATE TABLE order_items LIKE shop_source.order_items;
CREATE TABLE event_log LIKE shop_source.event_log;
CREATE TABLE big_ledger LIKE shop_source.big_ledger;

CREATE TABLE promo_codes (
    code       VARCHAR(20) PRIMARY KEY,
    percent    INT NOT NULL,
    expires_at DATE
);

-- The view also counts cancelled orders differently.
CREATE VIEW v_customer_orders AS
SELECT c.id AS customer_id, c.name, COUNT(o.id) AS orders, COALESCE(SUM(o.total), 0) AS total
FROM customers c LEFT JOIN orders o ON o.customer_id = c.id AND o.status <> 'cancelled'
GROUP BY c.id, c.name;

INSERT INTO customers (id, name, email, city, phone, created_at)
SELECT id, name, email, city, NULL, created_at FROM shop_source.customers WHERE id <> 5;
UPDATE customers SET email = 'budi.santoso@example.com' WHERE id = 2;
UPDATE customers SET city = 'Surabaya Timur', name = 'Citra L.' WHERE id = 3;
UPDATE customers SET city = 'Bogor' WHERE id = 4;
INSERT INTO customers (id, name, email, city, phone, created_at) VALUES
    (11, 'Kartika Putri', 'kartika@example.com', 'Padang', '0812000111', '2024-06-10 08:00:00');

INSERT INTO products SELECT * FROM shop_source.products;
INSERT INTO orders SELECT * FROM shop_source.orders WHERE id <> 5;
UPDATE orders SET status = 'paid' WHERE id = 3;
INSERT INTO order_items SELECT * FROM shop_source.order_items;
UPDATE order_items SET qty = 3 WHERE order_id = 1 AND line_no = 1;
INSERT INTO event_log SELECT * FROM shop_source.event_log;
UPDATE event_log SET message = 'order 2 delivered' WHERE source = 'shipping';
INSERT INTO promo_codes VALUES ('HEMAT10', 10, '2024-12-31');

INSERT INTO big_ledger SELECT * FROM shop_source.big_ledger;
UPDATE big_ledger SET amount = amount + 1 WHERE id IN (1234, 98765, 200001);
UPDATE big_ledger SET note = NULL WHERE id = 150000;
DELETE FROM big_ledger WHERE id IN (500, 180000);
INSERT INTO big_ledger VALUES (250001, 'ACC-0001', 10.00, 'late entry');
