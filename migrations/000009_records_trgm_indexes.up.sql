-- Поиск fullText — это ILIKE '%текст%'. Обычный B-tree-индекс для него бесполезен
-- (шаблон начинается с %), и Postgres читает всю таблицу. Триграммный GIN-индекс
-- из расширения pg_trgm умеет ускорять такой поиск.
--
-- Расширение ставим явно в public и на него же ссылаемся (public.gin_trgm_ops):
-- оно одно на всю БД, а миграции могут выполняться с другим search_path (так делают тесты).
CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public;

CREATE INDEX records_name_trgm_idx        ON records USING gin (name public.gin_trgm_ops);
CREATE INDEX records_description_trgm_idx ON records USING gin (description public.gin_trgm_ops);
