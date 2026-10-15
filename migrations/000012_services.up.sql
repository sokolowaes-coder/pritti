-- Услуги салона: что делают, сколько длится и сколько стоит.
CREATE TABLE services (
    id               uuid        PRIMARY KEY DEFAULT gen_uuid_v7(),
    name             TEXT        NOT NULL CHECK (btrim(name) <> ''),
    -- Длительность по умолчанию для записи на эту услугу.
    duration_minutes INT         NOT NULL CHECK (duration_minutes BETWEEN 5 AND 720),
    -- Деньги — целым числом в копейках, а не дробным: 0.1 + 0.2 во float не равно 0.3.
    price_kopecks    BIGINT      NOT NULL CHECK (price_kopecks >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- «Стрижка» и «стрижка» — одна и та же услуга.
CREATE UNIQUE INDEX services_name_key ON services (lower(name));

-- Услуга у записи необязательна: старые записи и записи «вне прайса» без неё.
ALTER TABLE records ADD COLUMN service_id uuid CONSTRAINT records_service_id_fkey REFERENCES services (id);
-- Индекс для проверки внешнего ключа при удалении услуги (как в 000006).
CREATE INDEX records_service_id_idx ON records (service_id);
