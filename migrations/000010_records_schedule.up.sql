-- Запись — это визит к мастеру: у неё есть время начала и конца и статус.
-- created_at остаётся, но означает только «когда запись внесли в систему».

-- btree_gist нужен, чтобы в одном GiST-индексе было и равенство uuid (тот же мастер),
-- и пересечение интервалов. Ставим в public и ссылаемся явно — как pg_trgm в 000009.
CREATE EXTENSION IF NOT EXISTS btree_gist SCHEMA public;

ALTER TABLE records
    ADD COLUMN starts_at TIMESTAMPTZ,
    ADD COLUMN ends_at   TIMESTAMPTZ,
    ADD COLUMN status    TEXT NOT NULL DEFAULT 'scheduled'
        CONSTRAINT records_status_check CHECK (status IN ('scheduled', 'completed', 'cancelled', 'no_show'));

-- У старых записей время визита неизвестно: берём время создания и длительность час,
-- но не дальше начала следующей записи того же мастера — иначе старые записи
-- пересеклись бы между собой и ограничение ниже не создалось бы.
-- LEAST в Postgres пропускает NULL: у последней записи мастера следующей нет — ровно час.
UPDATE records r
SET starts_at = r.created_at,
    ends_at   = LEAST(r.created_at + interval '1 hour', n.next_start)
FROM (
    SELECT id, lead(created_at) OVER (PARTITION BY employee_id ORDER BY created_at, id) AS next_start
    FROM records
) n
WHERE n.id = r.id;

ALTER TABLE records
    ALTER COLUMN starts_at SET NOT NULL,
    ALTER COLUMN ends_at   SET NOT NULL,
    ADD CONSTRAINT records_time_check CHECK (ends_at > starts_at),
    -- Главное правило расписания: у мастера не бывает двух записей в одно время.
    -- Проверяет сама БД, атомарно: два параллельных запроса на одно время не пройдут оба,
    -- как прошли бы при проверке «время свободно?» в Go и последующем INSERT.
    -- Отменённые не мешают: их время снова свободно. Интервалы [начало, конец) —
    -- запись 10:00–11:00 и следующая с 11:00 не пересекаются.
    ADD CONSTRAINT records_employee_no_overlap EXCLUDE USING gist (
        employee_id public.gist_uuid_ops WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (status <> 'cancelled');

-- Фильтры dateStart/dateEnd теперь по времени визита, а не по created_at.
DROP INDEX records_created_at_idx;
CREATE INDEX records_starts_at_idx ON records (starts_at);
