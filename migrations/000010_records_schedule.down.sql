DROP INDEX records_starts_at_idx;
CREATE INDEX records_created_at_idx ON records (created_at);

ALTER TABLE records
    DROP CONSTRAINT records_employee_no_overlap,
    DROP COLUMN starts_at,
    DROP COLUMN ends_at,
    DROP COLUMN status;
-- Расширение btree_gist не удаляем: им могут пользоваться и другие схемы этой БД.
