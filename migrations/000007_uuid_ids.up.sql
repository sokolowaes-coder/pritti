-- Переход с BIGINT-id на UUID v7: 36 символов, первые 48 бит — время создания в мс,
-- поэтому новые id растут по времени и хорошо ложатся в индекс.

-- gen_uuid_v7 собирает UUID v7: время ts + случайные биты из gen_random_uuid().
-- Своя функция, а не встроенная uuidv7(): та есть только с Postgres 18.
CREATE FUNCTION gen_uuid_v7(ts timestamptz DEFAULT clock_timestamp()) RETURNS uuid
LANGUAGE sql VOLATILE AS $$
    SELECT (lpad(to_hex(floor(extract(epoch FROM ts) * 1000)::bigint), 12, '0')
            || '7' || substr(r, 1, 3)      -- версия 7 + 12 случайных бит
            || substr(r, 17, 16))::uuid    -- вариант (8–b) + 60 случайных бит
    FROM (SELECT replace(gen_random_uuid()::text, '-', '') AS r) AS rnd
$$;

-- Новые id для уже существующих строк — из их created_at, чтобы порядок сохранился.
ALTER TABLE clients   ADD COLUMN uid uuid;
ALTER TABLE employees ADD COLUMN uid uuid;
ALTER TABLE records   ADD COLUMN uid uuid, ADD COLUMN client_uid uuid, ADD COLUMN employee_uid uuid;
UPDATE clients   SET uid = gen_uuid_v7(created_at);
UPDATE employees SET uid = gen_uuid_v7(created_at);
UPDATE records   SET uid = gen_uuid_v7(created_at);
UPDATE records r SET client_uid   = c.uid FROM clients c   WHERE r.client_id = c.id;
UPDATE records r SET employee_uid = e.uid FROM employees e WHERE r.employee_id = e.id;

-- Старые колонки удаляем вместе с их ключами и индексами, новые ставим на их место.
ALTER TABLE records DROP COLUMN client_id, DROP COLUMN employee_id, DROP COLUMN id;
ALTER TABLE clients   DROP COLUMN id;
ALTER TABLE employees DROP COLUMN id;

ALTER TABLE clients   RENAME COLUMN uid TO id;
ALTER TABLE employees RENAME COLUMN uid TO id;
ALTER TABLE records   RENAME COLUMN uid TO id;
ALTER TABLE records   RENAME COLUMN client_uid TO client_id;
ALTER TABLE records   RENAME COLUMN employee_uid TO employee_id;

ALTER TABLE clients   ALTER COLUMN id SET DEFAULT gen_uuid_v7(), ADD CONSTRAINT clients_pkey   PRIMARY KEY (id);
ALTER TABLE employees ALTER COLUMN id SET DEFAULT gen_uuid_v7(), ADD CONSTRAINT employees_pkey PRIMARY KEY (id);
ALTER TABLE records   ALTER COLUMN id SET DEFAULT gen_uuid_v7(), ADD CONSTRAINT records_pkey   PRIMARY KEY (id);

ALTER TABLE records
    ADD CONSTRAINT records_client_id_fkey   FOREIGN KEY (client_id)   REFERENCES clients (id),
    ADD CONSTRAINT records_employee_id_fkey FOREIGN KEY (employee_id) REFERENCES employees (id);
CREATE INDEX records_client_id_idx   ON records (client_id);
CREATE INDEX records_employee_id_idx ON records (employee_id);
