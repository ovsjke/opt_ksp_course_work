# Запись на приём к специалисту

Учебный веб-сервис для курсовой работы по дисциплине «Оптимизация клиент-серверных приложений».

В сервисе можно войти в систему, посмотреть свои записи, выбрать услугу и свободный слот, записаться к специалисту, отменить запись и посмотреть общую сводку.

Это исходная baseline-версия для последующих замеров. В ней специально нет кеша, дополнительных индексов для ускорения, очередей и фоновых задач.

## Стек

- Go 1.25.14 в Docker;
- Go 1.27.1 для локальной проверки;
- `net/http`, `chi`;
- `pgx/v5`, `pgxpool`;
- PostgreSQL 16;
- `html/template`;
- bcrypt;
- Docker Compose 5.1.1.

## Требования

Для обычного запуска нужны Docker и Docker Compose. Для запуска без Docker и тестов нужен Go. Также для smoke-проверки используются `sh` и `curl`.

По умолчанию приложение занимает порт `8080`, а PostgreSQL — `5432`.

## Запуск

Из корня проекта:

```sh
cp .env.example .env
docker compose up -d --build
docker compose exec app ./seed --mode small
```

После запуска приложение доступно по адресу <http://localhost:8080/login>.

Учётная запись для проверки:

```text
login: demo
password: demo
```

Полезные команды:

```sh
# Состояние контейнеров
docker compose ps

# Проверка приложения
curl -fsS http://localhost:8080/healthz

# Логи
docker compose logs app

# Остановка с сохранением данных
docker compose down
```

Миграции применяются автоматически перед запуском приложения.

## Переменные окружения

Пример настроек находится в `.env.example`.

| Переменная | Назначение |
| --- | --- |
| `APP_PORT` | порт приложения |
| `DATABASE_URL` | строка подключения к PostgreSQL для локальных Go-команд |
| `SESSION_TTL` | время жизни пользовательской сессии |
| `DB_NAME` | имя базы данных |
| `DB_USER` | пользователь PostgreSQL |
| `DB_PASSWORD` | пароль PostgreSQL; `changeme` в примере нужно заменить |
| `DB_PORT` | внешний порт PostgreSQL |
| `TEST_DATABASE_URL` | строка подключения к отдельной тестовой БД |

Файл `.env` не добавляется в Git. Если меняется пароль или порт базы, нужно также обновить `DATABASE_URL`.

## Структура проекта

```text
cmd/
  app/          запуск HTTP-сервера
  migrate/      запуск миграций
  seed/         заполнение базы
  measure/      замеры HTTP API
  dbstats/      замеры времени PostgreSQL
internal/
  config/       конфигурация
  domain/       структуры и общие ошибки
  repository/   SQL и работа с PostgreSQL
  service/      бизнес-логика
  handler/      HTTP handlers
  middleware/   middleware авторизации
  session/      серверные сессии
  measurement/  HTTP-клиент для замеров
migrations/     SQL-миграции
seed/           генерация тестовых данных
templates/      HTML-шаблоны
static/         CSS
tests/          HTTP и интеграционные тесты
scripts/        вспомогательные скрипты
```

Основная цепочка вызовов:

```text
HTTP request
    ↓
handler
    ↓
service
    ↓
repository
    ↓
PostgreSQL
```

## Данные и бизнес-правила

Предметные сущности:

- `Specialist` — специалист;
- `Service` — услуга;
- `Slot` — слот расписания;
- `Appointment` — запись на приём.

Технические таблицы `users` и `sessions` нужны для авторизации. Таблица `specialist_services` хранит связь между специалистами и услугами.

При создании записи сервис проверяет:

- существует ли слот и услуга;
- оказывает ли специалист выбранную услугу;
- подходит ли длительность слота;
- нет ли активной записи на пересекающийся интервал.

Бронирование выполняется в транзакции. Для защиты от конкурентных запросов строка специалиста блокируется через `FOR UPDATE`. После отмены запись получает статус `cancelled`, а слот снова становится доступным.

## Авторизация

Авторизация сделана через серверные сессии:

1. приложение проверяет логин и bcrypt-хеш пароля;
2. создаёт случайный `session_id`;
3. сохраняет сессию в PostgreSQL;
4. отправляет клиенту cookie `session_id`.

Cookie имеет флаги `HttpOnly` и `SameSite=Lax`. Сессии хранятся в базе, поэтому не пропадают после перезапуска приложения.

Пример входа через API:

```sh
curl -i -c cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"login":"demo","password":"demo"}' \
  http://localhost:8080/api/login
```

## Миграции

При запуске через Compose миграции применяются автоматически. Их также можно запустить вручную:

```sh
docker compose exec app ./migrate
```

Для локального запуска сначала экспортируйте настройки из `.env`:

```sh
set -a
. ./.env
set +a
go run ./cmd/migrate
```

Миграции создают схему `pavel_ovsyannikov` и расширение `pg_stat_statements`.

## Наполнение базы

Seed полностью заменяет данные приложения и удаляет текущие сессии. Генерация воспроизводимая: используются фиксированные даты и random seed.

Малое наполнение:

```sh
docker compose exec app ./seed --mode small
```

Создаётся 10 специалистов, 10 услуг, 500 слотов и 300 записей.

Рабочее наполнение:

```sh
docker compose exec app ./seed --mode working
```

Создаётся 200 специалистов, 30 услуг, 200 000 слотов и 75 000 записей.

Локальный вариант команды:

```sh
go run ./cmd/seed --mode small
go run ./cmd/seed --mode working
```

## Тесты

Основная проверка:

```sh
go test ./...
go vet ./...
```

Тесты проверяют бронирование, конфликты и пересечения, отмену, длительность слота, конкурентные запросы, dashboard, авторизацию, HTTP-контракт и повторяемость seed.

Интеграционные тесты используют отдельную базу с суффиксом `_test`:

```sh
docker compose exec db sh -c 'createdb -U "$POSTGRES_USER" pavel_ovsyannikov_test'
set -a
. ./.env
set +a
export TEST_DATABASE_URL="postgres://${DB_USER}:${DB_PASSWORD}@localhost:${DB_PORT}/pavel_ovsyannikov_test?sslmode=disable"
go test -count=1 ./...
```

Тесты откажутся очищать базу, имя которой не заканчивается на `_test`.

Быстрая проверка основных маршрутов:

```sh
./scripts/smoke.sh
```

## Замеры времени ответа

Утилита `measure` выполняет один прогревочный запрос, а затем не менее 20 измеряемых запросов для каждой операции:

```sh
# Small
docker compose exec app ./seed --mode small
docker compose exec app ./measure --profile small --n 20

# Working
docker compose exec app ./seed --mode working
docker compose exec app ./measure --profile working --n 20
```

В выводе показываются `p50`, `p95`, максимальное время и размер ответа. Измеряются:

- список записей и список с фильтром;
- карточка записи;
- услуги и специалисты;
- свободные слоты;
- создание и отмена записи;
- dashboard;
- login и logout.

Для создания и отмены записей подготовка и очистка выполняются вне измеряемого интервала. После полного запуска инструмент сравнивает количество строк до и после. Успешная проверка заканчивается строкой:

```text
state check: PASS
```

Таким образом dashboard и остальные read-only операции измеряются на исходном наборе данных.

## Разделение времени приложения и БД

Утилита `dbstats` делает warm-up, сбрасывает `pg_stat_statements`, выполняет один исследуемый HTTP-запрос и выводит:

- полное время запроса;
- суммарное время SQL;
- примерное время приложения и сети;
- количество SQL-вызовов;
- самые дорогие SQL-запросы.

Примеры:

```sh
docker compose exec app ./dbstats \
  --method GET \
  --path '/api/appointments?page=1&size=20'

docker compose exec app ./dbstats \
  --method GET \
  --path '/api/dashboard?date_from=2026-10-01&date_to=2026-10-08'
```

Для POST можно передать JSON через `--body`. Для мутаций warm-up должен использовать отдельные данные:

```sh
docker compose exec app ./seed --mode small
docker compose exec app ./dbstats \
  --method POST \
  --path /api/appointments \
  --warmup-body '{"slot_id":3,"service_id":1}' \
  --body '{"slot_id":7,"service_id":1}'
```

`dbstats` изменяет состояние при POST, поэтому перед повторяемой серией нужно снова выполнить seed.

## JSON API

Все `/api/*`, кроме login, требуют cookie с действующей сессией. Ошибки возвращаются в виде:

```json
{
  "error": "описание ошибки"
}
```

| Метод и путь | Параметры | Ответ | Основные ошибки |
| --- | --- | --- | --- |
| `POST /api/login` | JSON: `login`, `password` | статус входа и cookie | `400`, `401` |
| `POST /api/logout` | — | статус выхода | `401` |
| `GET /api/appointments` | `page`, `size`, `specialist_id`, `service_id`, `status`, `date_from`, `date_to` | `items`, `total`, `page`, `size` | `400`, `401` |
| `GET /api/appointments/{id}` | ID записи | запись со специалистом, услугой и слотом | `400`, `401`, `404` |
| `GET /api/services` | — | список услуг | `401` |
| `GET /api/specialists` | — | список специалистов | `401` |
| `GET /api/slots` | `service_id`, `date_from`, `date_to` | список свободных слотов | `400`, `401`, `404` |
| `POST /api/appointments` | JSON: `slot_id`, `service_id` | созданная запись | `400`, `401`, `404`, `409` |
| `POST /api/appointments/{id}/cancel` | ID записи | статус `cancelled` | `400`, `401`, `404` |
| `GET /api/dashboard` | `date_from`, `date_to` | статистика и загрузка специалистов | `400`, `401` |
| `GET /healthz` | без авторизации | состояние приложения и маркер проекта | — |

Пример работы:

```sh
# Вход
curl -c cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"login":"demo","password":"demo"}' \
  http://localhost:8080/api/login

# Список записей
curl -b cookies.txt \
  'http://localhost:8080/api/appointments?page=1&size=20'

# Свободные слоты
curl -b cookies.txt \
  'http://localhost:8080/api/slots?service_id=1&date_from=2026-10-01&date_to=2026-10-08'

# Бронирование: подставьте свободный slot_id из предыдущего ответа
curl -b cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"slot_id":7,"service_id":1}' \
  http://localhost:8080/api/appointments
```

## Что пока не оптимизировалось

В baseline-версии оставлены обычные прямолинейные запросы:

- `JOIN` и отдельный `COUNT(*)`;
- пагинация через `LIMIT/OFFSET`;
- `NOT EXISTS` при поиске свободных слотов;
- прямой расчёт агрегатов dashboard;
- отсутствие дополнительных performance-индексов;
- отсутствие кеша и фоновой обработки.

Это исходная версия для следующих заданий по поиску узких мест и оптимизации.
