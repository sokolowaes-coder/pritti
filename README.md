# go-education

Учебный REST API на Go для работы с записями: `net/http` + PostgreSQL (`pgx`).

## Запуск

Всё в Docker — nginx, API и база (http://localhost:8080):

```sh
echo "API_KEY=$(openssl rand -hex 32)" > .env   # один раз: ключ к API (.env в .gitignore)
docker compose up -d --build   # собрать и запустить
docker compose logs -f app     # логи сервера
docker compose down            # остановить (данные БД сохраняются)
docker compose down -v         # остановить и удалить данные
```

Снаружи доступен только nginx ([deploy/nginx/nginx.conf](deploy/nginx/nginx.conf)): он проксирует запросы
в app, ограничивает частоту (20 запросов/с с одного IP, иначе `429`) и размер тела, скрывает версию
и добавляет защитные заголовки. У app портов наружу нет, Postgres слушает только `127.0.0.1:5433`.
Без `API_KEY` в `.env` API работает без авторизации.

Если порт 8080 занят: `HTTP_PORT=8081 docker compose up -d`.

Для разработки — только база в Docker, сервер через `go run`:

```sh
docker compose up -d db
DATABASE_URL="postgres://app:app@localhost:5433/go_education?sslmode=disable" go run ./cmd/server
```

Порт 8080 тогда должен быть свободен: если запущен весь compose, остановите его nginx (`docker compose stop nginx`).

Переменные окружения сервера:

| Переменная     | По умолчанию | Описание                                  |
|----------------|--------------|-------------------------------------------|
| `DATABASE_URL` | —            | строка подключения к Postgres (обязательна) |
| `PORT`         | `8080`       | порт HTTP-сервера                         |
| `LOG_LEVEL`    | `info`       | `debug`, `info`, `warn`, `error`          |
| `LOG_FORMAT`   | текст        | `json` — логи в JSON                      |
| `APP_TZ`       | системный    | часовой пояс для дат в фильтрах, напр. `Europe/Moscow` |
| `DATABASE_URL_UNPOOLED` | —   | прямое подключение для миграций, если `DATABASE_URL` идёт через пулер |
| `API_KEY`      | —            | если задан, все запросы, кроме `/healthz` и Swagger, требуют заголовок `X-API-Key` |

По Ctrl+C / `docker stop` сервер дожидается текущих запросов и завершается корректно.

## API

Swagger UI: [localhost:8080/swagger](http://localhost:8080/swagger) — все методы, схемы и кнопка «Try it out».
Описание лежит в [internal/docs/openapi.yaml](internal/docs/openapi.yaml): при изменении API обновляйте и его.

| Метод    | Путь            | Тело               | Ответ                        |
|----------|-----------------|--------------------|------------------------------|
| `GET`    | `/records`      | —                  | `200` список с фильтрами (см. ниже) |
| `POST`   | `/records`      | `{"name":"...", "description":"...", "client_id":1, "employee_id":1}` | `201` созданная запись |
| `GET`    | `/records/{id}` | —                  | `200` запись                 |
| `PUT`    | `/records/{id}` | `{"name":"...", "description":"...", "client_id":1, "employee_id":1}` | `200` обновлённая запись |
| `DELETE` | `/records/{id}` | —                  | `204` без тела               |
| `GET`    | `/healthz`      | —                  | `200` `{"status":"ok"}` / `503`, если БД недоступна |

У записи `name`, `client_id` и `employee_id` обязательны, `description` — нет (по умолчанию пустая строка).
`PUT` заменяет все поля. Клиент и сотрудник должны существовать — сначала создайте их через
`POST /clients` и `POST /employees`. У записей, созданных до появления клиентов, `client_id` и `employee_id` равны `null`.

### Клиенты и сотрудники

Два независимых ресурса с одинаковым набором методов: `GET` (список), `POST`, `PUT /{id}`, `DELETE /{id}`.

| Ресурс       | Тело                                            | Обязательно           |
|--------------|-------------------------------------------------|-----------------------|
| `/clients`   | `{"full_name":"Иванова Анна","phone":"8 (916) 123-45-67"}` | `full_name`, `phone` |
| `/employees` | `{"name":"Мария Смирнова"}`                     | `name`                |

Телефон клиента принимается в любом привычном виде (`+7…`, `8…`, со скобками, пробелами и дефисами)
и сохраняется как `+79161234567`. Два клиента с одним телефоном — `409`.
Удалить клиента или сотрудника, на которого ссылаются записи, тоже нельзя — `409`.
Список: `{"success": true, "count": N, "data": [...]}`.

Ошибки: `400` — неверный id, JSON, фильтр, неизвестное поле в теле, пустое или слишком длинное значение
(имена — до 200 символов, `description` — до 2000), нет `client_id`/`employee_id` или такого клиента/сотрудника нет;
`401` — нет верного `X-API-Key` (если задан `API_KEY`); `404` — записи (клиента, сотрудника) нет;
`409` — конфликт (занятый телефон, удаление клиента/сотрудника с записями); `413` — тело больше 1 МБ; `500` — ошибка сервера.
Тело ошибки всегда JSON: `{"success": false, "error": "запись не найдена"}`.

### Список: фильтры и пагинация

`GET /records` принимает query-параметры, все необязательные:

| Параметр         | Пример                          | Что делает                                      |
|------------------|---------------------------------|-------------------------------------------------|
| `fullText`       | `лазер`                         | поиск подстроки без учёта регистра              |
| `fullTextFields` | `name` или `name,description`   | где искать (по умолчанию — везде)               |
| `id`             | `1,2,3` или `id=1&id=2`         | только записи с этими id                        |
| `dateStart`      | `2026-09-27` / `2026-09-27T10:00:00` | `created_at` не раньше                     |
| `dateEnd`        | `2026-09-27`                    | `created_at` не позже; дата без времени — до конца дня |
| `limit`          | `20`                            | размер страницы, 1–100 (по умолчанию 20)        |
| `offset`         | `0`                             | сколько записей пропустить                      |

Даты без пояса считаются в часовом поясе `APP_TZ` (в Docker — `Europe/Moscow`); можно передать и с поясом: `2026-09-27T10:00:00+03:00`.

```sh
curl 'localhost:8080/records?fullText=лазер&dateStart=2026-09-27&dateEnd=2026-09-27&limit=10'
```

```json
{
  "success": true,
  "filters": {
    "fullText":  {"value": "лазер", "fields": null},
    "id":        {"value": null},
    "dateStart": {"value": "2026-09-27T00:00:00"},
    "dateEnd":   {"value": "2026-09-27T23:59:59"}
  },
  "count": 1,
  "pagination": {"limit": 10, "offset": 0},
  "data": [
    {"id": 7, "name": "Лазерная депиляция", "description": "", "created_at": "2026-09-27T08:00:00Z"}
  ]
}
```

`filters` показывает, какие фильтры применились (`null` — не задан), `count` — сколько записей подходит всего, `data` — текущая страница.

Запись:

```json
{"id": 1, "name": "первая", "description": "", "client_id": 1, "employee_id": 1, "created_at": "2026-09-26T23:33:25.360074+03:00"}
```

Примеры:

С включённым `API_KEY` добавьте к каждому запросу `-H "X-API-Key: $API_KEY"`.

```sh
curl -i -X POST localhost:8080/clients -d '{"full_name":"Анна Иванова","phone":"89161234567"}'
curl -i -X POST localhost:8080/employees -d '{"name":"Мария"}'
curl -i -X POST localhost:8080/records -d '{"name":"первая","client_id":1,"employee_id":1}'
curl -i localhost:8080/records
curl -i localhost:8080/records/1
curl -i -X PUT localhost:8080/records/1 -d '{"name":"изменённая","client_id":1,"employee_id":1}'
curl -i -X DELETE localhost:8080/records/1
```

## Деплой на Vercel

Vercel сам распознаёт Go-сервер ([cmd/server](cmd/server/main.go)) и запускает его как обычный
долгоживущий процесс — отдельной конфигурации не нужно. Порт Vercel передаёт через `PORT`.

1. [vercel.com/new](https://vercel.com/new) → импортировать репозиторий, настройки по умолчанию.
2. В проекте: **Storage → Create Database → Neon** (Postgres) → подключить к проекту.
   Vercel сам добавит `DATABASE_URL` и `DATABASE_URL_UNPOOLED`.
3. **Settings → Environment Variables**: `APP_TZ` = `Europe/Moscow`, `API_KEY` = длинная случайная строка
   (`openssl rand -hex 32`) — без него API в интернете открыт всем.
4. **Deployments → Redeploy**. Миграции применятся при старте.

Дальше каждый push в `main` деплоится автоматически.

## Логи и health-check

Каждый запрос пишется в лог ([slog](https://pkg.go.dev/log/slog)):

```
level=INFO msg=request method=POST path=/records status=201 duration=944µs
```

Паника в хендлере не роняет соединение: клиент получает `500`, в лог — ошибка со стеком.
Docker проверяет контейнер через `/server healthcheck` (запрос к `/healthz`) — статус виден в `docker compose ps`.

## Миграции

SQL-миграции лежат в [migrations/](migrations/) и встроены в бинарник (`embed`).
При старте сервер сам применяет новые через [golang-migrate](https://github.com/golang-migrate/migrate);
применённые версии хранятся в таблице `schema_migrations`.

Чтобы изменить схему, добавь пару файлов со следующим номером:

```
migrations/000003_что_делает.up.sql     -- изменение
migrations/000003_что_делает.down.sql   -- откат
```

Уже применённые миграции не редактируй — только добавляй новые.

## Тесты

```sh
# тесты хендлеров — база не нужна
go test ./...

# плюс тесты хранилища на настоящей БД (нужен docker compose up -d)
TEST_DATABASE_URL="postgres://app:app@localhost:5433/go_education?sslmode=disable" go test -race ./...
```

Тесты хранилища работают во временной схеме и не трогают данные в базе.

На каждый push в `main` GitHub Actions запускает gofmt, `go vet`, все тесты с Postgres и сборку Docker-образа ([.github/workflows/ci.yml](.github/workflows/ci.yml)).

## Структура

```
cmd/server/          точка входа: HTTP-сервер, graceful shutdown (Docker и Vercel)
internal/app/        сборка приложения: миграции, БД, роуты, middleware
internal/middleware/ логирование запросов, recover от паник
internal/health/     /healthz и проверка для Docker
internal/records/
  model.go           структура Record
  filter.go          фильтры списка и формат ответа
  storage.go         SQL-запросы к Postgres
  handler.go         HTTP-хендлеры и роуты
internal/clients/    клиенты: модель, SQL, хендлеры (так же устроен internal/employees/)
internal/employees/  сотрудники
internal/docs/       openapi.yaml и страница Swagger UI (/swagger)
internal/httpx/      общие хелперы хендлеров: JSON-ответы, ошибки, разбор id и тела
internal/testdb/     временная схема в Postgres для тестов хранилищ
migrations/          SQL-миграции + код их применения
Dockerfile           сборка образа сервера (multi-stage, ~12 МБ)
docker-compose.yml   nginx (порт 8080) → app → PostgreSQL (127.0.0.1:5433)
deploy/nginx/        конфиг nginx
.github/workflows/   CI
```
