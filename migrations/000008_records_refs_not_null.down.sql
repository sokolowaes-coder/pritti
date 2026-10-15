-- Удалённые записи не вернуть: откатывается только ограничение.
ALTER TABLE records
    ALTER COLUMN client_id   DROP NOT NULL,
    ALTER COLUMN employee_id DROP NOT NULL;
