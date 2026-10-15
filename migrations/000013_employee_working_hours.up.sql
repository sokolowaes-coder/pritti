-- Рабочие часы мастера: по одному интервалу на день недели, по местному времени салона
-- (APP_TZ). Перерывы и разные графики по неделям — когда понадобятся, отдельной таблицей.
CREATE TABLE employee_working_hours (
    -- Удалили мастера — его график больше не нужен.
    employee_id uuid     NOT NULL REFERENCES employees (id) ON DELETE CASCADE,
    weekday     SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7), -- ISO 8601: 1 — пн, 7 — вс
    start_time  TIME     NOT NULL,
    end_time    TIME     NOT NULL,
    PRIMARY KEY (employee_id, weekday),
    CHECK (end_time > start_time)
);
