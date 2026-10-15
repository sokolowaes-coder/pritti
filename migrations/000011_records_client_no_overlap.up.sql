-- Клиент не может быть в двух местах сразу: записи одного клиента не пересекаются,
-- как и записи мастера (000010). Отменённые не в счёт.
--
-- Если пересечения уже есть, ограничение не создастся. Чтобы не гадать по сообщению
-- Postgres, сначала проверяем сами и останавливаемся с понятной ошибкой: какие записи
-- конфликтуют, решает человек (отменить одну или перенести), а не миграция.
DO $$
DECLARE
    conflicts int;
BEGIN
    SELECT count(*) INTO conflicts
    FROM records a
    JOIN records b ON a.client_id = b.client_id AND a.id < b.id
    WHERE a.status <> 'cancelled' AND b.status <> 'cancelled'
      AND tstzrange(a.starts_at, a.ends_at) && tstzrange(b.starts_at, b.ends_at);

    IF conflicts > 0 THEN
        RAISE EXCEPTION 'пересекающихся пар записей у клиентов: % — отмените или перенесите их и запустите миграцию снова', conflicts
            USING HINT = 'найти: SELECT a.id, b.id FROM records a JOIN records b ON a.client_id = b.client_id AND a.id < b.id '
                      || 'WHERE a.status <> ''cancelled'' AND b.status <> ''cancelled'' '
                      || 'AND tstzrange(a.starts_at, a.ends_at) && tstzrange(b.starts_at, b.ends_at)';
    END IF;
END $$;

ALTER TABLE records
    ADD CONSTRAINT records_client_no_overlap EXCLUDE USING gist (
        client_id public.gist_uuid_ops WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (status <> 'cancelled');
