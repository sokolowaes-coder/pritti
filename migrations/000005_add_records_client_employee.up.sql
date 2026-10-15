-- Колонки допускают NULL только ради записей, созданных до этой миграции.
-- Новые записи без client_id и employee_id не пропускает API.
ALTER TABLE records
    ADD COLUMN client_id   BIGINT CONSTRAINT records_client_id_fkey   REFERENCES clients (id),
    ADD COLUMN employee_id BIGINT CONSTRAINT records_employee_id_fkey REFERENCES employees (id);
