# Запись на приём к специалисту — pavel_ovsyannikov

Учебный baseline для курсовой по «Оптимизации клиент-серверных приложений». Сервис хранит расписание, бронирует и отменяет записи, показывает личный список и общую загрузку специалистов. Следующие этапы работы — измерение SQL/HTTP и поиск узких мест. Здесь нет кешей, дополнительных performance-индексов, очередей и предварительных агрегатов.

## Стек и версии

Фактически проверено: Go **1.27.1** (локальные команды и тесты, darwin/arm64), Go **1.25.14** (версия из собранного app-бинарника Docker), Docker Compose **5.1.1**. Стек: net/http, chi v5.2.3, pgx v5.7.6 / pgxpool, PostgreSQL 16, bcrypt (x/crypto v0.41.0), html/template. Директива `go 1.25.0` в go.mod задаёт минимальную версию языка, а не фактический toolchain. Версии Go-зависимостей закреплены в go.mod/go.sum. Тег Docker `golang:1.25-alpine` плавающий; для сравнения замеров фиксируйте фактическую версию Go, digest образов и параметры компьютера.

## Требования

Docker Engine / Docker Desktop; проверенная версия Docker Compose — 5.1.1. Для запуска вне контейнера и тестов использован Go 1.27.1. Для smoke-скрипта — POSIX shell и curl. Порты 8080 и 5432 должны быть свободны либо изменены в `.env`.

## Установка и запуск

Все команды выполнять из корня репозитория:

```sh
cp .env.example .env
docker compose up -d --build
docker compose ps
docker compose exec app ./seed --mode small
```

Открыть <http://localhost:8080/login>. Миграции применяются автоматически перед запуском HTTP-сервера; app стартует после успешного healthcheck базы. После первой сборки достаточно `docker compose up -d`.

```sh
curl -fsS http://localhost:8080/healthz
docker compose logs app
# Остановить, сохранив данные:
docker compose down
```

База и схема называются `pavel_ovsyannikov`. Compose project называется так же. `/healthz` и startup log содержат этот маркер. Healthcheck app проверяет HTTP-процесс; readiness БД проверяется при запуске. Ошибки БД при работе API возвращаются как 500.

Локальный Go-процесс при контейнерной базе:

```sh
docker compose up -d db
set -a
. ./.env
set +a
go run ./cmd/migrate
go run ./cmd/seed --mode small
go run ./cmd/app
```

Если app уже работает в Docker, остановить его командой `docker compose stop app` перед локальным запуском на том же порту. Go-команды читают переменные окружения; `.env` автоматически читает только Compose.

## Переменные окружения

| Переменная | Значение в примере / назначение |
| --- | --- |
| APP_PORT | 8080: локальный HTTP-порт и внешний порт Compose; внутри app контейнера всегда 8080 |
| DATABASE_URL | PostgreSQL DSN для Go-команд; в Compose app формируется из DB_* и имени хоста db |
| SESSION_TTL | 24h; положительная Go duration, минимум 1s |
| DB_NAME | pavel_ovsyannikov |
| DB_USER | pavel_ovsyannikov |
| DB_PASSWORD | changeme — пример-заполнитель; задайте свой пароль и обновите DATABASE_URL |
| DB_PORT | 5432: опубликованный порт PostgreSQL |
| TEST_DATABASE_URL | Отдельная БД с суффиксом `_test` для интеграционных тестов |

`.env` исключён из Git. Настоящие секреты в репозитории отсутствуют. Если меняете DB_PORT или DB_PASSWORD, обновите также локальный DATABASE_URL. Спецсимволы в пароле в DSN требуют URL-кодирования. Изменение DB_* не переименовывает существующую БД в уже созданном Docker volume.

## Архитектура и правила

```text
cmd/
  app/          HTTP-сервер, graceful shutdown, slog
  migrate/      применение SQL-миграций
  seed/         наполнение базы
  measure/      серия HTTP-измерений
  dbstats/      один запрос + pg_stat_statements
internal/
  config/       переменные окружения
  domain/       структуры, ошибки, правила интервалов
  repository/   pgxpool, SQL, транзакции
  service/      операции бронирования/отмены
  session/      bcrypt, crypto/rand, серверные сессии
  middleware/   авторизация, Cache-Control: no-store
  handler/      chi, JSON и HTML
  measurement/  HTTP-клиент с cookie jar
migrations/     SQL + встроенный runner
seed/           детерминированный генератор
static/         один CSS
templates/      server-side HTML
tests/          HTTP и PostgreSQL integration tests
scripts/        smoke-проверка
```

HTTP → service / session → repository → PostgreSQL. Для простых чтений handler использует Store сервиса; бизнес-операции идут через методы сервиса. Интерфейсы Store позволяют тестировать HTTP без БД. Шаблоны разбираются при старте, данные приложения не кешируются. Session ID — 32 случайных байта в hex, хранится в БД; middleware проверяет срок действия на каждом защищённом запросе. Перезапуск app сессии не сбрасывает. Logout удаляет серверную сессию и cookie.

Четыре предметные сущности: **Specialist** (специалист, `specialists`), **Service** (услуга, `services`), **Slot** (слот расписания, `slots`), **Appointment** (запись на приём, `appointments`). `users` и `sessions` — технические таблицы авторизации. `specialist_services` — таблица связи M:N, задающая доступные услуги специалиста.

* Запись занимает **весь слот**, даже если услуга короче. Интервалы `[starts_at, ends_at)`; соседние слоты допустимы.
* Услуга должна поддерживаться специалистом; длительность слота должна покрывать duration_minutes. Специалист определяется по slot_id, клиент не задаёт его отдельно.
* Создание: транзакция READ COMMITTED → блокировка строки специалиста `FOR UPDATE` → проверка поддержки услуги, длительности и пересечений → INSERT → COMMIT. Запрос после получения блокировки видит уже завершившиеся конкурентные бронирования. Изменение расписания через API не предусмотрено.
* Отмена идемпотентна: сохраняет первую cancelled_at, освобождает слот. Чужая запись и несуществующая запись одинаково возвращают 404.
* Список/карточка/отмена — только свои записи. Сводка — общая для всех специалистов стенда. Регистрации, ролей и административного UI нет.
* Даты относятся к **началу слота**, а не created_at. Нижняя граница включена, верхняя исключена. `YYYY-MM-DD` трактуется как полночь UTC; также допустим RFC3339 со смещением. Отсутствующие границы: 2000-01-01 и 2100-01-01.
* Для воспроизводимости допускается бронировать исторические слоты: проверка относительно текущих часов отсутствует. Seed не сдвигается со временем.
* Загрузка: сумма минут booked-слотов / сумма минут всех слотов специалиста за выбранный период × 100. У специалистов без слотов — 0%. Если вручную завести перекрывающиеся слоты, знаменатель суммирует их длительности, а не объединение интервалов. Seed создаёт непересекающееся расписание.
* Доля отмен: cancelled / total × 100, при total=0 — 0. Отменённые записи сохраняются в истории и учитываются в total.

## Миграции

```sh
docker compose exec app ./migrate
# Или с экспортированным DATABASE_URL:
go run ./cmd/migrate
```

`migrations/*.sql` встроены в бинарник и применяются по имени в одной транзакции. Применённые имена хранятся в `public.pavel_ovsyannikov_migrations`. Advisory lock сериализует одновременные запуски runner; это не механизм блокировки бронирования. Повторный запуск безопасен. Автоматических down-миграций нет.

Compose включает `shared_preload_libraries=pg_stat_statements`; SQL создаёт extension. При внешнем PostgreSQL эти настройки и права на CREATE EXTENSION нужно обеспечить самостоятельно. Схема содержит только PK, FK, необходимые UNIQUE и CHECK; внешние ключи не сопровождаются дополнительными индексами. Защита от пересечений обеспечивается приложением в транзакции; прямой ручной INSERT обязан соблюдать тот же протокол.

## Seed small

```sh
docker compose exec app ./seed --mode small
# Локально:
go run ./cmd/seed --mode small
```

10 специалистов, 10 услуг, 500 слотов, 300 записей, один demo user. Около четверти записей отменены. Расписание начинается **2026-10-01 08:00 UTC**, по восемь часовых слотов в день на специалиста. Длительности услуг — 15/30/45/60 минут, каждый специалист поддерживает только часть услуг.

## Seed working

```sh
docker compose exec app ./seed --mode working
# Локально:
go run ./cmd/seed --mode working
```

200 специалистов, 30 услуг, 200000 слотов, 75000 записей. Рабочий период: 2026-10-01 — 2027-02-02 включительно. Используются `rand.NewSource(42)`, фиксированная дата и фиксированный bcrypt-хеш учебного пароля; значения строк и ID повторяются. COPY загружает данные в одной транзакции, последовательности ID устанавливаются после загрузки.

**Seed заменяет все данные приложения и удаляет сессии.** Запускать на выделенном учебном стенде без параллельного трафика. После запуска напечатаны количества строк каждой таблицы. Нужно снова войти. Для одинакового начального состояния перед каждой сравнительной серией заново выполните seed нужного режима.

## Авторизация и demo account

Логин: **demo**, пароль: **demo**. В users хранится bcrypt-хеш. Cookie `session_id`: Path=/, HttpOnly, SameSite=Lax; для localhost Secure=false.

```sh
curl -i -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"login":"demo","password":"demo"}' http://localhost:8080/api/login
curl -b cookies.txt 'http://localhost:8080/api/appointments?page=1&size=20'
curl -b cookies.txt -X POST http://localhost:8080/api/logout
```

HTML: GET/POST `/login`, POST `/logout`, GET `/appointments`, GET `/appointments/{id}`, GET `/booking`, GET `/dashboard`, POST `/appointments`, POST `/appointments/{id}/cancel`. HTML-формы используют application/x-www-form-urlencoded, ошибки операций показываются как JSON с соответствующим HTTP-кодом. Без сессии HTML перенаправляет на `/login`; API возвращает 401. На всех ответах Cache-Control: no-store.

## Запуск тестов

```sh
go test ./...
go vet ./...
```

Без БД выполняются проверки чистых правил интервалов/длительности/процентов и HTTP-контракта, логина, cookie, logout, истечения сессии, HTML и валидации. PostgreSQL-тест пропускается с явной причиной, если TEST_DATABASE_URL не задан.

Полная проверка транзакций на отдельной **пустой** тестовой БД:

```sh
# Один раз создать отдельную БД:
docker compose exec db sh -c 'createdb -U "$POSTGRES_USER" pavel_ovsyannikov_test'
set -a
. ./.env
set +a
export TEST_DATABASE_URL="postgres://${DB_USER}:${DB_PASSWORD}@localhost:${DB_PORT}/pavel_ovsyannikov_test?sslmode=disable"
go test -count=1 ./...
```

При повторных проверках выполнять последние команды без createdb. Тест отказывается сбрасывать БД без суффикса `_test`. Он проверяет свободный/занятый/пересекающийся/слишком короткий слот, отмену, неподдерживаемую услугу, соседние интервалы, 12 конкурентных бронирований, метрики, доступ к чужой записи, сохранение сессий при новом соединении и повторяемость seed. Каждый сценарий сам готовит данные; порядок запуска не важен. Не запускать одновременно несколько процессов интеграционных тестов против одной тестовой БД.

Проверка маршрутов поднятого стенда:

```sh
./scripts/smoke.sh
```

## Запуск measurement tool

```sh
docker compose exec app ./measure --profile small --n 20
# Локально:
go run ./cmd/measure --profile small --n 20
# После seed working:
docker compose exec app ./measure --profile working --n 20
```

`--base http://localhost:8080` меняет адрес. Profile — метка режима ожидаемого seed; инструмент сам seed не выполняет. По умолчанию выполняется **полный режим**, включая операции авторизации. Для каждой строки — отдельный warm-up, затем минимум 20 последовательных измеряемых запросов. Вывод: p50/p95 по nearest-rank, max и min/max размера тела в байтах (без заголовков). Время — от отправки до полного чтения тела ответа.

| Строка измерения | Операция контракта |
| --- | --- |
| appointments first page | GET /api/appointments?page=1&size=20 |
| appointments filtered | GET /api/appointments с фильтром specialist_id=1, status=booked и периодом |
| appointment detail | GET /api/appointments/{id} |
| services | GET /api/services |
| specialists | GET /api/specialists |
| available slots | GET /api/slots?service_id=1 с периодом |
| book appointment | POST /api/appointments |
| cancel appointment | POST /api/appointments/{id}/cancel |
| dashboard | GET /api/dashboard с периодом |
| login | POST /api/login |
| logout | POST /api/logout |

Все бизнес-операции используют одну исходную сессию. Login/logout измеряются отдельным клиентом с собственным cookie jar: после каждого login сессия удаляется вне замера, перед каждым logout создаётся новая сессия вне замера. Таким образом logout всегда удаляет действующую сессию. Первоначальная авторизация бизнес-клиента также не входит в замеры.

Период фильтра/слотов/dashboard фиксирован: `[2026-10-01, 2026-10-08)` UTC. Полный run сохраняет исходные предметные данные. Для setup/cleanup инструмент подключается напрямую к PostgreSQL через `DATABASE_URL` (в Compose уже задан). Локально перед `go run ./cmd/measure` экспортируйте переменные из `.env`, как в разделе запуска. Соответствие HTTP-сервера этой БД проверяется по созданной сессии до любых изменений предметных данных.

Перед каждым warm-up и измеряемым запросом мутации создаётся временная копия подходящего свободного слота; исходный слот и его история не изменяются. Для POST бронирования измеряется только HTTP-запрос; после остановки таймера удаляются созданная benchmark-запись и временный слот. Приватный slot_id позволяет выполнить точечную очистку даже при ошибке HTTP-ответа без appointment ID. Для отмены setup напрямую создаёт отдельную booked-запись; измеряется только HTTP cancel. Затем запись восстанавливается в booked с cancelled_at=NULL, после чего временные запись и слот удаляются. Вся подготовка, восстановление и удаление выполняются вне измеряемого интервала, включая warm-up. Очистка запускается также при обычных ошибках запроса; её ошибки не скрываются.

Каждый read-only endpoint, включая dashboard, измеряется на исходном dataset. После logout выводятся counts до/после для users, specialists, services, specialist_services, slots, appointments, sessions и числа booked/cancelled. Несовпадение завершает инструмент с ошибкой; успешная проверка выводит `state check: PASS`. Повторный seed после успешного запуска больше не нужен. Счётчики identity-последовательностей продолжают расти: инструмент не откатывает выдачу ID. Предметные строки и их значения сохраняются; физическое состояние PostgreSQL и его статистика после запросов не идентичны исходным.

Контрольный small-run: [вывод измерений](measurements/small-measure.txt), [снимок до](measurements/small-before.txt), [снимок после](measurements/small-after.txt). Counts и хеши значений строк всех семи таблиц совпали; снимки получены независимым [SQL-запросом](measurements/state.sql).

Запускайте измерения на выделенном стенде без параллельного изменения данных. После принудительного завершения процесса (например, SIGKILL) или недоступности БД во время cleanup нельзя гарантировать очистку; перед следующей серией в таком случае восстановите исходный seed. Эти ограничения не меняют обработку обычного полного запуска.


Измерение DB/application time:

```sh
docker compose exec app ./dbstats --method GET --path '/api/appointments?page=1&size=20'
docker compose exec app ./dbstats --method GET --path '/api/dashboard?date_from=2026-10-01&date_to=2026-10-08'
# Локально с экспортированным DATABASE_URL:
go run ./cmd/dbstats --method GET --path '/api/appointments?page=1&size=20'
```

`--method` задаёт HTTP-метод (по умолчанию GET), `--path` — путь `/api/...`, `--body` — JSON-тело, передаваемое без дополнительной строковой обёртки. Без `--body` тело отсутствует. `--warmup-path` и `--warmup-body` позволяют прогреть тот же метод на отдельных тестовых данных; по умолчанию совпадают с исследуемым запросом.

**Перед каждым следующим POST-примером заново выполните small seed.** ID ниже относятся только к исходному small-набору: слоты 3 и 7 свободны для услуги 1, записи 2 и 3 имеют статус booked. Для working или изменённой базы сначала выберите свои свободные слоты/активные записи через API. Два POST при работе dbstats изменяют состояние: warm-up и одна исследуемая операция. Сам dbstats seed/откат не выполняет. Повторное бронирование того же слота даст 409; повторная отмена той же записи измерит идемпотентный повтор. Поэтому для этих операций обязательно используйте **разные** исходные данные прогрева и измерения:

```sh
# Бронирование: warm-up занимает слот 3, измеряется бронирование слота 7.
docker compose exec app ./seed --mode small
docker compose exec app ./dbstats --method POST --path /api/appointments \
  --warmup-body '{"slot_id":3,"service_id":1}' \
  --body '{"slot_id":7,"service_id":1}'

# Отмена: обе записи изначально booked; прогрев отменяет 2, замер отменяет 3.
docker compose exec app ./seed --mode small
docker compose exec app ./dbstats --method POST \
  --warmup-path /api/appointments/2/cancel --path /api/appointments/3/cancel

# Операции авторизации тоже поддерживаются.
docker compose exec app ./dbstats --method POST --path /api/login \
  --body '{"login":"demo","password":"demo"}'
docker compose exec app ./dbstats --method POST --path /api/logout

# Восстановить исходное состояние после мутаций:
docker compose exec app ./seed --mode small
```

Порядок: login → один warm-up → подготовка сессии, если измеряется login/logout → reset pg_stat_statements → **ровно одна исследуемая HTTP-операция** → один снимок статистики. Между reset и снимком нет запросов подготовки/очистки. Для login прогретая сессия удаляется до reset; для logout после warm-up создаётся новая действующая сессия до reset. Завершение оставшейся сессии происходит после снимка.

Вывод: полное HTTP-время, сумма total_exec_time, SQL calls (включая проверку сессии и транзакционные команды), до 10 дорогих SQL и остаток `total - DB`. Учитываются top-level SQL текущей БД: их время уже включает работу триггеров, поэтому вложенные SQL не суммируются повторно. Служебные запросы к pg_stat_statements исключены.

Это приближение: остаток включает сеть, ожидание соединения, планирование SQL и работу Go. pg_stat_statements глобален; инструмент предназначен для **изолированного стенда без параллельных запросов и seed**, reset очищает статистику всего инстанса. Нужен пользователь с правами reset (POSTGRES_USER в Compose их имеет). Отрицательный остаток может указывать на посторонний трафик; инструмент его не скрывает. Подробности полей — в [официальной документации PostgreSQL 16](https://www.postgresql.org/docs/16/pgstatstatements.html); интерфейс COPY/pool — в [документации pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).

## Программный интерфейс

API принимает JSON, возвращает `application/json`. Все пути `/api/*`, кроме login, требуют session cookie. Любой защищённый endpoint может вернуть 401; ошибка инфраструктуры — 500 с нейтральным сообщением. Ошибка: `{"error":"описание"}`. Неизвестные JSON-поля, лишние JSON-значения, неправильные типы и тело более 8 KiB отклоняются с 400.

| Метод и путь | Параметры | Ответ | Ошибки |
| --- | --- | --- | --- |
| POST /api/login | JSON login, password | 200 `{"status":"ok"}` + Set-Cookie | 400, 401 |
| POST /api/logout | — | 200 `{"status":"ok"}`, удаляет cookie/session | 401 |
| GET /api/appointments | page=1, size=20 (1..100), specialist_id, service_id, status=booked/cancelled, date_from, date_to | 200 Page | 400, 401 |
| GET /api/appointments/{id} | положительный id | 200 Appointment со связанными объектами | 400, 401, 404 |
| GET /api/services | — | 200 массив Service | 401 |
| GET /api/specialists | — | 200 массив Specialist | 401 |
| GET /api/slots | обязательный service_id; date_from, date_to | 200 массив Slot, первые 200 по starts_at,id | 400, 401, 404 (услуга) |
| POST /api/appointments | JSON slot_id, service_id, положительные целые | 201 Appointment | 400 (длительность/поддержка/вход), 401, 404 (слот/услуга), 409 (занят/пересечение) |
| POST /api/appointments/{id}/cancel | положительный id | 200 `{"status":"cancelled"}` | 400, 401, 404 |
| GET /api/dashboard | date_from, date_to | 200 Dashboard | 400, 401 |
| GET /healthz | без авторизации | 200 `{"status":"ok","project":"pavel_ovsyannikov"}` | — |

Page: `{"items":[],"total":0,"page":1,"size":20}`. Список сортируется по starts_at,id, использует LIMIT/OFFSET; page за пределами списка возвращает пустой items, не 404. Пустые массивы — `[]`, не null. size/page и query ID ограничены положительными числами до 10000000; size дополнительно ≤100.

Service: `{"id":1,"name":"Консультация 01","duration_minutes":15}`.
Specialist: `{"id":1,"name":"Специалист 001"}`.
Slot: `{"id":1,"specialist_id":1,"starts_at":"2026-10-01T08:00:00Z","ends_at":"2026-10-01T09:00:00Z"}`.

Appointment:

```json
{
  "id": 301,
  "user_id": 1,
  "specialist_id": 1,
  "service_id": 1,
  "slot_id": 1,
  "status": "booked",
  "created_at": "2026-09-27T12:00:00Z",
  "cancelled_at": null,
  "specialist": {"id": 1, "name": "Специалист 001"},
  "service": {"id": 1, "name": "Консультация 01", "duration_minutes": 15},
  "slot": {"id": 1, "specialist_id": 1, "starts_at": "2026-10-01T08:00:00Z", "ends_at": "2026-10-01T09:00:00Z"}
}
```

Это пример формы ответа; конкретный слот может быть занят seed-записью. Для бронирования сначала получите свободный слот:

```sh
curl -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"login":"demo","password":"demo"}' http://localhost:8080/api/login
curl -b cookies.txt 'http://localhost:8080/api/slots?service_id=1&date_from=2026-10-01&date_to=2026-10-08'
# Подставить ID из предыдущего ответа:
SLOT_ID=1
curl -b cookies.txt -H 'Content-Type: application/json' \
  -d "{\"slot_id\":$SLOT_ID,\"service_id\":1}" http://localhost:8080/api/appointments
```

Dashboard: `{"total":4,"booked":3,"cancelled":1,"cancellation_rate":25,"specialists":[{"specialist_id":1,"name":"Специалист 001","booked_minutes":180,"scheduled_minutes":480,"utilization_percent":37.5}]}`. Все проценты — 0..100 для непересекающегося исходного расписания, не доли 0..1. Время сериализуется в RFC3339, возможны дробные секунды.

## Что намеренно оставлено для оптимизации

Прямые JOIN/COUNT, отдельные запросы COUNT и страницы, OFFSET-пагинация, NOT EXISTS для поиска свободных слотов и коррелированные агрегаты загрузки специалистов. Нет индексов по датам/status/FK ради скорости, кеша данных и браузерного кеширования, фоновой очистки сессий, предварительных вычислений и материализованных представлений. Чтение total и items не использует общий snapshot и при параллельных изменениях может отражать соседние состояния. Сводка также состоит из двух запросов. Блокировка специалиста сериализует все его бронирования, в том числе непересекающиеся; это простой механизм корректности baseline.

Сначала фиксируйте нагрузку и результаты измерений, затем меняйте один фактор за раз. Искусственных задержек и намеренно сломанных алгоритмов нет.
