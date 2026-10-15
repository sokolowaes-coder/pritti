-- Обратно на BIGINT: новые числовые id выдаются по порядку created_at.
ALTER TABLE clients   ADD COLUMN nid bigint;
ALTER TABLE employees ADD COLUMN nid bigint;
ALTER TABLE records   ADD COLUMN nid bigint, ADD COLUMN client_nid bigint, ADD COLUMN employee_nid bigint;
UPDATE clients c   SET nid = n.rn FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn FROM clients) n   WHERE c.id = n.id;
UPDATE employees e SET nid = n.rn FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn FROM employees) n WHERE e.id = n.id;
UPDATE records r   SET nid = n.rn FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn FROM records) n   WHERE r.id = n.id;
UPDATE records r SET client_nid   = c.nid FROM clients c   WHERE r.client_id = c.id;
UPDATE records r SET employee_nid = e.nid FROM employees e WHERE r.employee_id = e.id;

ALTER TABLE records DROP COLUMN client_id, DROP COLUMN employee_id, DROP COLUMN id;
ALTER TABLE clients   DROP COLUMN id;
ALTER TABLE employees DROP COLUMN id;

ALTER TABLE clients   RENAME COLUMN nid TO id;
ALTER TABLE employees RENAME COLUMN nid TO id;
ALTER TABLE records   RENAME COLUMN nid TO id;
ALTER TABLE records   RENAME COLUMN client_nid TO client_id;
ALTER TABLE records   RENAME COLUMN employee_nid TO employee_id;

-- Счётчик identity продолжает с max(id) + 1.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['clients', 'employees', 'records'] LOOP
        EXECUTE format('ALTER TABLE %I ALTER COLUMN id SET NOT NULL', t);
        EXECUTE format('ALTER TABLE %I ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY', t);
        EXECUTE format('SELECT setval(pg_get_serial_sequence(%L, ''id''), coalesce(max(id), 0) + 1, false) FROM %I', t, t);
        EXECUTE format('ALTER TABLE %I ADD PRIMARY KEY (id)', t);
    END LOOP;
END $$;

ALTER TABLE records
    ADD CONSTRAINT records_client_id_fkey   FOREIGN KEY (client_id)   REFERENCES clients (id),
    ADD CONSTRAINT records_employee_id_fkey FOREIGN KEY (employee_id) REFERENCES employees (id);
CREATE INDEX records_client_id_idx   ON records (client_id);
CREATE INDEX records_employee_id_idx ON records (employee_id);

DROP FUNCTION gen_uuid_v7(timestamptz);
