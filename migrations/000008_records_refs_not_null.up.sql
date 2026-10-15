-- Записи без клиента или сотрудника остались с тех времён, когда их ещё не было
-- (см. 000005). API такие давно не создаёт; удаляем их, чтобы правило «у записи
-- всегда есть клиент и сотрудник» проверяла сама БД, а не только Go-код.
DELETE FROM records WHERE client_id IS NULL OR employee_id IS NULL;

ALTER TABLE records
    ALTER COLUMN client_id   SET NOT NULL,
    ALTER COLUMN employee_id SET NOT NULL;
