-- Postgres не создаёт индексы на внешние ключи сам. Без них удаление клиента
-- или сотрудника (проверка, что на него нет ссылок) читает всю таблицу records.
CREATE INDEX records_client_id_idx   ON records (client_id);
CREATE INDEX records_employee_id_idx ON records (employee_id);
-- Фильтр списка по dateStart / dateEnd.
CREATE INDEX records_created_at_idx  ON records (created_at);
