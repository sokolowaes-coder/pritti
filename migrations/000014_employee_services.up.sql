-- Какие услуги оказывает мастер (многие ко многим): Анна — стрижка и окрашивание,
-- Мария — маникюр.
--
-- Что запись на услугу делается только к мастеру, который её оказывает, проверяет
-- сервисный слой (records.Booking), а не внешний ключ records → employee_services:
-- такой ключ не дал бы убрать услугу у мастера, пока у него есть хоть одна старая
-- запись на неё, а прошлые визиты остаются верными, даже если услугу он больше не делает.
CREATE TABLE employee_services (
    employee_id uuid NOT NULL REFERENCES employees (id) ON DELETE CASCADE,
    service_id  uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    PRIMARY KEY (employee_id, service_id)
);
-- PRIMARY KEY начинается с employee_id; для «кто оказывает услугу» нужен индекс по service_id.
CREATE INDEX employee_services_service_id_idx ON employee_services (service_id);
