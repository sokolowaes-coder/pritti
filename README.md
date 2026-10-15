# Pritti

Сервис онлайн-записи к мастерам: услуги, мастера с графиком, клиенты и записи без накладок по времени.
REST API на Go — `net/http` + PostgreSQL (`pgx`).

- API: https://api.pritti.space
- Swagger: https://api.pritti.space/swagger

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
в app, ограничивает частоту и размер тела и скрывает версию. У app портов наружу нет,
Postgres слушает только `127.0.0.1:5433`. Без `API_KEY` в `.env` compose не запустится.
В compose сервер сам применяет миграции при старте (`MIGRATE_ON_START=true`).

Если порт 8080 занят: `HTTP_PORT=8081 docker compose up -d`.

Демо-данные — услуги, три мастера с графиком и услугами, клиенты, прошедшие и будущие записи
(ставятся в свободные окна из `/slots`):

```sh
API_KEY=$(grep ^API_KEY= .env | cut -d= -f2) ./scripts/seed.sh   # на пустую базу; нужны curl и jq
```

Для разработки — только база в Docker, сервер через `go run`:

```sh
docker compose up -d db
DATABASE_URL="postgres://app:app@localhost:5433/pritti?sslmode=disable" \
  AUTH_DISABLED=true MIGRATE_ON_START=true go run ./cmd/server
```

Порт 8080 тогда должен быть свободен: если запущен весь compose, остановите его nginx (`docker compose stop nginx`).

Переменные окружения сервера:

| Переменная     | По умолчанию | Описание                                  |
|----------------|--------------|-------------------------------------------|
| `DATABASE_URL` | —            | строка подключения к Postgres (обязательна) |
| `PORT`         | `8080`       | порт HTTP-сервера                         |
| `LOG_LEVEL`    | `info`       | `debug`, `info`, `warn`, `error`          |
| `LOG_FORMAT`   | текст        | `json` — логи в JSON                      |
| `APP_TZ`       | системный    | часовой пояс для времени без пояса (фильтры, `starts_at`), напр. `Europe/Moscow` |
| `DATABASE_URL_UNPOOLED` | —   | прямое подключение для миграций, если `DATABASE_URL` идёт через пулер |
| `API_KEY`      | —            | **обязателен**: все запросы, кроме `/healthz` и Swagger, требуют заголовок `X-API-Key` |
| `AUTH_DISABLED` | `false`     | `true` — работать без `API_KEY` (только для локальной разработки) |
| `MIGRATE_ON_START` | `false`  | `true` — применять миграции при старте; иначе сервер только проверяет, что схема БД не отстала |
| `RATE_LIMIT_RPS` | `20`       | запросов в секунду с одного IP; `0` — без лимита |
| `RATE_LIMIT_BURST` | `40`     | сколько запросов можно всплеском сверх среднего |
| `REAL_IP_HEADER` | — (на Vercel `X-Real-IP`) | заголовок, где прокси передаёт IP клиента; без прокси не задавайте — заголовок подделывается |

Без `API_KEY` (и без `AUTH_DISABLED=true`) сервер не стартует: забытая переменная на проде
не должна молча открыть API всем.

По Ctrl+C / `docker stop` сервер дожидается текущих запросов и завершается корректно.

## API

Обычный сценарий записи:

1. `GET /services` — выбрать услугу;
2. `GET /services/{id}/employees` — кто её оказывает;
3. `GET /slots?employeeId=…&date=2026-10-20&serviceId=…` — когда мастер свободен;
4. `POST /records` с `service_id` и `starts_at` — название и конец возьмутся из услуги.

Swagger UI: [localhost:8080/swagger](http://localhost:8080/swagger) — все методы, схемы и кнопка «Try it out».
Описание лежит в [internal/docs/openapi.yaml](internal/docs/openapi.yaml): при изменении API обновляйте и его.

| Метод    | Путь            | Тело               | Ответ                        |
|----------|-----------------|--------------------|------------------------------|
| `GET`    | `/records`      | —                  | `200` список с фильтрами (см. ниже) |
| `POST`   | `/records`      | запись (см. ниже)  | `201` созданная запись       |
| `GET`    | `/records/{id}` | —                  | `200` запись                 |
| `PUT`    | `/records/{id}` | запись (см. ниже)  | `200` обновлённая запись     |
| `DELETE` | `/records/{id}` | —                  | `204` без тела               |
| `GET`    | `/healthz`      | —                  | `200` `{"status":"ok"}` / `503`, если БД недоступна |

Запись — визит клиента к мастеру:

```json
{"name": "Чистка лица", "description": "кабинет 1", "client_id": "<uuid>", "employee_id": "<uuid>",
 "starts_at": "2026-10-17T10:00:00", "ends_at": "2026-10-17T11:00:00", "status": "scheduled"}
```

Обязательны `client_id`, `employee_id`, `starts_at`. С `service_id` можно не передавать `name` и `ends_at` —
возьмутся название услуги и начало плюс её длительность; без услуги оба обязательны.
Время — без пояса (тогда в `APP_TZ`) или с поясом: `2026-10-17T10:00:00+03:00`.
`ends_at` позже `starts_at`, визит не дольше 12 часов.
`status`: `scheduled` (по умолчанию), `completed` — пришёл, `cancelled` — отменена, `no_show` — не пришёл;
`completed` и `no_show` — только для визита, который уже начался.

**Записать на услугу можно только к мастеру, который её оказывает** — иначе `400` с ошибкой поля `service_id`.
Если мастер перестал оказывать услугу, его прошлые записи на неё остаются, их можно отмечать:
проверка срабатывает только при новой записи или смене мастера или услуги.
`PUT` заменяет все поля. Клиент и сотрудник должны существовать — сначала создайте их через
`POST /clients` и `POST /employees`.

**У мастера не бывает двух записей в одно время** — иначе `409 employee_busy`, **у клиента тоже** —
`409 client_busy`. Это проверяет сама БД (exclusion constraint в миграциях 000010 и 000011), поэтому
правило держится и при одновременных запросах.
Отменённые записи время не занимают; интервалы полуоткрытые — запись 10:00–11:00 и следующая с 11:00 не конфликтуют.

### Клиенты и сотрудники

Два независимых ресурса с одинаковым набором методов: `GET` (список), `GET /{id}`, `POST`, `PUT /{id}`, `DELETE /{id}`.

| Ресурс       | Тело                                            | Обязательно           |
|--------------|-------------------------------------------------|-----------------------|
| `/clients`   | `{"full_name":"Иванова Анна","phone":"8 (916) 123-45-67"}` | `full_name`, `phone` |
| `/employees` | `{"name":"Мария Смирнова"}`                     | `name`                |

Телефон клиента принимается в любом привычном виде (`+7…`, `8…`, со скобками, пробелами и дефисами)
и сохраняется как `+79161234567`. Два клиента с одним телефоном — `409`.
Удалить клиента или сотрудника, на которого ссылаются записи, тоже нельзя — `409`.
Список постраничный, как у записей (`limit` 1–100, по умолчанию 20, и `offset`):
`{"success": true, "count": N, "pagination": {"limit": 20, "offset": 0}, "data": [...]}`, где `count` — сколько всего.

### Услуги

`/services` — `GET` (список по алфавиту, постранично), `GET /{id}`, `POST`, `PUT /{id}`, `DELETE /{id}`:

```json
{"name": "Стрижка", "duration_minutes": 60, "price_kopecks": 150000}
```

Цена — целым числом в копейках (150000 — 1500 ₽): дробные деньги во float теряют точность.
Длительность — 5–720 минут. Названия уникальны без учёта регистра (`409 service_name_taken`).
Удалить услугу, на которую есть записи, нельзя — `409 service_has_records`.

Какие услуги оказывает мастер — `GET /employees/{id}/services`, задать набор целиком —
`PUT /employees/{id}/services` с `{"service_ids": ["<uuid>", ...]}`. Кто оказывает услугу — `GET /services/{id}/employees`.

### График и свободное время

Недельный график мастера — `GET /employees/{id}/schedule`, задать целиком — `PUT` (одной транзакцией):

```json
{"days": [{"weekday": 1, "start": "10:00", "end": "19:00"}, {"weekday": 6, "start": "11:00", "end": "16:00"}]}
```

`weekday` — 1 (понедельник) … 7 (воскресенье), время — по часам салона (`APP_TZ`). Дней, которых нет в списке,
мастер не работает.

Свободное время — `GET /slots?employeeId=<uuid>&date=2026-10-20&serviceId=<uuid>` (или `&duration=60` в минутах):
окна внутри рабочих часов, без пересечений с записями мастера и не в прошлом, с шагом 15 минут.

```json
{"success": true, "employee_id": "…", "date": "2026-10-20", "duration_minutes": 60,
 "data": [{"starts_at": "2026-10-20T10:00:00+03:00", "ends_at": "2026-10-20T11:00:00+03:00"}, …]}
```

### Ошибки

Тело ошибки всегда JSON: `{"success": false, "code": "record_not_found", "error": "запись не найдена"}`.
Программы проверяют `code` — он не меняется; `error` — текст для людей, его можно переписать.

Ошибки полей приходят все сразу — `400 validation_error` с `fields`:

```json
{"success": false, "code": "validation_error",
 "error": "ends_at: должно быть позже starts_at; name: обязательное поле",
 "fields": {"ends_at": "должно быть позже starts_at", "name": "обязательное поле"}}
```

| Статус | Коды |
|--------|------|
| `400`  | `invalid_id`, `invalid_json` (сломанный JSON, неизвестное поле), `invalid_query` (фильтр), `validation_error` |
| `401`  | `unauthorized` — нет верного `X-API-Key` |
| `404`  | `record_not_found`, `client_not_found`, `employee_not_found`, `service_not_found` |
| `409`  | `employee_busy`, `client_busy`, `phone_taken`, `service_name_taken`, `client_has_records`, `employee_has_records`, `service_has_records` |
| `413`  | `body_too_large` — тело больше 1 МБ |
| `429`  | `rate_limited` — больше 20 запросов/с с одного IP |
| `500`  | `internal_error` — подробности только в логе сервера |

В каждом ответе есть заголовок `X-Request-ID`: по нему запрос находится в логах.

### Список: фильтры и пагинация

`GET /records` принимает query-параметры, все необязательные:

| Параметр         | Пример                          | Что делает                                      |
|------------------|---------------------------------|-------------------------------------------------|
| `fullText`       | `лазер`                         | поиск подстроки без учёта регистра              |
| `fullTextFields` | `name` или `name,description`   | где искать (по умолчанию — везде)               |
| `id`             | `<uuid>,<uuid>` или `id=<uuid>&id=<uuid>` | только записи с этими id                |
| `employeeId`     | `<uuid>`                        | только записи этих мастеров                     |
| `clientId`       | `<uuid>`                        | только записи этих клиентов                     |
| `status`         | `scheduled,completed`           | только с этими статусами                        |
| `dateStart`      | `2026-09-27` / `2026-09-27T10:00:00` | визит начинается не раньше                 |
| `dateEnd`        | `2026-09-27`                    | визит начинается не позже; дата без времени — до конца дня |
| `limit`          | `20`                            | размер страницы, 1–100 (по умолчанию 20)        |
| `offset`         | `0`                             | сколько записей пропустить                      |

Даты без пояса считаются в часовом поясе `APP_TZ` (в Docker — `Europe/Moscow`); можно передать и с поясом: `2026-09-27T10:00:00+03:00`.

Расписание мастера на день:

```sh
curl 'localhost:8080/records?employeeId=<uuid>&status=scheduled&dateStart=2026-10-17&dateEnd=2026-10-17'
```

```json
{
  "success": true,
  "filters": {
    "fullText":   {"value": null, "fields": null},
    "id":         {"value": null},
    "employeeId": {"value": ["01a140dc-0b22-7d3f-8e5a-6b7c8d9e0f1a"]},
    "clientId":   {"value": null},
    "status":     {"value": ["scheduled"]},
    "dateStart":  {"value": "2026-10-17T00:00:00+03:00"},
    "dateEnd":    {"value": "2026-10-17T23:59:59+03:00"}
  },
  "count": 1,
  "pagination": {"limit": 20, "offset": 0},
  "data": [
    {"id": "01a140dc-0c34-7abb-8d6c-36e2d1879e0d", "name": "Чистка лица", "description": "",
     "client_id": "01a140dc-0b11-7c2e-9d4f-5a6b7c8d9e0f", "employee_id": "01a140dc-0b22-7d3f-8e5a-6b7c8d9e0f1a",
     "starts_at": "2026-10-17T07:00:00Z", "ends_at": "2026-10-17T08:00:00Z", "status": "scheduled",
     "created_at": "2026-10-15T19:40:12.360074Z"}
  ]
}
```

`filters` показывает, какие фильтры применились (`null` — не задан), `count` — сколько записей подходит всего, `data` — текущая страница.

Все id — UUID v7 (`01a140dc-0c34-7abb-8d6c-36e2d1879e0d`): первые 48 бит — время создания,
поэтому id растут по времени и не выдают, сколько записей в базе. Генерирует их Postgres
(функция `gen_uuid_v7()` из миграции 000007). Записи сортируются по `starts_at`, клиенты и сотрудники — по `created_at`.

Примеры:

Добавьте к каждому запросу `-H "X-API-Key: $API_KEY"`.

```sh
curl -i -X POST localhost:8080/clients -d '{"full_name":"Анна Иванова","phone":"89161234567"}'   # → id клиента
curl -i -X POST localhost:8080/employees -d '{"name":"Мария"}'                                   # → id сотрудника
curl -i -X POST localhost:8080/records -d '{"name":"первая","client_id":"<id клиента>","employee_id":"<id сотрудника>","starts_at":"2026-10-17T10:00:00","ends_at":"2026-10-17T11:00:00"}'
curl -i localhost:8080/records
curl -i localhost:8080/records/<id записи>
curl -i -X PUT localhost:8080/records/<id записи> -d '{"name":"первая","client_id":"<id клиента>","employee_id":"<id сотрудника>","starts_at":"2026-10-17T10:00:00","ends_at":"2026-10-17T11:00:00","status":"completed"}'
curl -i -X DELETE localhost:8080/records/<id записи>
```

## Деплой на Vercel

Vercel сам распознаёт Go-сервер ([cmd/server](cmd/server/main.go)) и запускает его как обычный
долгоживущий процесс — отдельной конфигурации не нужно. Порт Vercel передаёт через `PORT`.

1. [vercel.com/new](https://vercel.com/new) → импортировать репозиторий, настройки по умолчанию.
2. В проекте: **Storage → Create Database → Neon** (Postgres) → подключить к проекту.
   Vercel сам добавит `DATABASE_URL` и `DATABASE_URL_UNPOOLED`.
3. **Settings → Environment Variables**: `APP_TZ` = `Europe/Moscow`, `API_KEY` = длинная случайная строка
   (`openssl rand -hex 32`) — без него сервер не стартует.
4. В GitHub: **Settings → Secrets and variables → Actions** → секрет `DATABASE_URL_UNPOOLED`
   (то же значение, что у Vercel).
5. **Actions → Migrate → Run workflow** — применить миграции, затем **Deployments → Redeploy** на Vercel.

Дальше каждый push в `main` деплоится автоматически. Если в push есть новые миграции,
запустите **Migrate**: пока схема БД отстаёт от кода, новый сервер не стартует
(«схема БД на версии N, код ждёт M»), а не ломается на первом запросе.

На Vercel нет nginx: лимит запросов, защитные заголовки и таймауты делает сам сервер.
IP клиента для лимита берётся из `X-Real-IP` — его ставит Vercel. Лимит считается в памяти
каждого экземпляра сервера отдельно — это защита от перебора и флуда, а не точная квота.

## Логи и health-check

Каждый запрос пишется в лог ([slog](https://pkg.go.dev/log/slog)):

```
level=INFO msg=request request_id=3f9a1c0be47d2a85 method=POST path=/records status=201 duration=944µs
```

У всех строк одного запроса (сам запрос, ошибка, паника) один `request_id`; клиент видит его
в заголовке ответа `X-Request-ID`. За nginx id генерирует nginx.

Паника в хендлере не роняет соединение: клиент получает `500`, в лог — ошибка со стеком.
Docker проверяет контейнер через `/server healthcheck` (запрос к `/healthz`) — статус виден в `docker compose ps`.

## Миграции

SQL-миграции лежат в [migrations/](migrations/) и встроены в бинарник (`embed`), применяются
через [golang-migrate](https://github.com/golang-migrate/migrate); применённые версии хранятся в таблице `schema_migrations`.

```sh
DATABASE_URL=... go run ./cmd/server migrate   # применить новые миграции и выйти
```

На проде это отдельный осознанный шаг (workflow **Migrate**), а не побочный эффект деплоя:
миграция может менять и удалять данные. Сервер при старте только сверяет версию схемы
и не запускается, если БД отстала. Локально и в compose удобнее `MIGRATE_ON_START=true`.

Чтобы изменить схему, добавь пару файлов со следующим номером:

```
migrations/000003_что_делает.up.sql     -- изменение
migrations/000003_что_делает.down.sql   -- откат
```

Уже применённые миграции не редактируй — только добавляй новые.

Миграция 000011 (запрет пересечений у клиента) остановится с понятной ошибкой, если такие пересечения
уже есть в данных: какие записи отменить или перенести, решает человек, а не миграция.

Миграции 000009 и 000010 ставят расширения `pg_trgm` (триграммные индексы для поиска `fullText`)
и `btree_gist` (запрет пересечения записей мастера). Оба есть в стандартном образе Postgres и в Neon;
у роли БД должно быть право `CREATE` на базу.

## Тесты

```sh
# тесты хендлеров — база не нужна
go test ./...

# плюс тесты хранилища на настоящей БД (нужен docker compose up -d)
TEST_DATABASE_URL="postgres://app:app@localhost:5433/pritti?sslmode=disable" go test -race ./...
```

Тесты хранилища работают во временной схеме и не трогают данные в базе.

На каждый push в `main` GitHub Actions запускает gofmt, `go vet`, staticcheck, govulncheck (уязвимости
в Go и зависимостях), все тесты с Postgres и сборку Docker-образа ([.github/workflows/ci.yml](.github/workflows/ci.yml)).
Dependabot раз в неделю предлагает обновления зависимостей ([.github/dependabot.yml](.github/dependabot.yml)).

## Структура

```
cmd/server/          точка входа: HTTP-сервер, graceful shutdown; команды healthcheck и migrate
internal/app/        конфиг из окружения, сборка приложения: БД, роуты, middleware
internal/middleware/ request-id, логирование, recover, защитные заголовки, лимит запросов, API-ключ
internal/health/     /healthz и проверка для Docker
internal/records/    записи — три слоя:
  handler.go         HTTP: разбор и проверка формата, коды ответов; /records и /slots
  booking.go         сервисный слой: правила записи (услуга, мастер, длительность, статус), свободное время
  storage.go         SQL-запросы к Postgres
  slots.go           подбор свободных окон — чистая функция без БД
  model.go, filter.go  структура Record, фильтры списка и формат ответа
internal/services/   услуги: модель, SQL, хендлеры
internal/clients/    клиенты: модель, SQL, хендлеры (так же устроен internal/employees/)
internal/employees/  сотрудники, их график и услуги
internal/docs/       openapi.yaml и страница Swagger UI (/swagger)
internal/httpx/      общие хелперы хендлеров: JSON-ответы, коды ошибок, валидация, разбор id, тела и страниц
internal/testdb/     временная схема в Postgres для тестов хранилищ
migrations/          SQL-миграции + код их применения
Dockerfile           сборка образа сервера (multi-stage, ~12 МБ)
docker-compose.yml   nginx (порт 8080) → app → PostgreSQL (127.0.0.1:5433)
deploy/nginx/        конфиг nginx
.github/workflows/   CI и ручной запуск миграций
```
