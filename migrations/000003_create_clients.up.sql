CREATE TABLE clients (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    full_name  TEXT        NOT NULL CHECK (btrim(full_name) <> ''),
    -- Телефон хранится уже нормализованным: +7 и 10 цифр. Приводит к этому виду API.
    phone      TEXT        NOT NULL CHECK (phone ~ '^\+7[0-9]{10}$')
                           CONSTRAINT clients_phone_key UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
