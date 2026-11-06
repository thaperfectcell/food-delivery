# FoodFlow — система управления заказами и доставкой еды для ресторанов

Курсовая работа по дисциплине «Архитектура клиент-серверных приложений»
(РТУ МИРЭА, ИКБО-10-24, Таганов А. А.).

Прототип (MVP) построен на **микросервисной архитектуре** с **API Gateway**;
каждый сервис внутри организован по **слоистой архитектуре**
(транспорт → бизнес-логика → доступ к данным).

| Технология | Назначение |
|---|---|
| Go 1.24 | все сервисы и API Gateway |
| REST API (JSON) | внешний API для веб-клиента (`api/openapi.yaml`) |
| gRPC + Protocol Buffers | синхронное взаимодействие gateway → сервисы и order → restaurant (`proto/`) |
| NATS JetStream | асинхронные доменные события, хореографическая сага |
| PostgreSQL 16 | отдельная база данных у каждого сервиса |
| Docker, Docker Compose | контейнеризация и локальное развёртывание |
| GitHub Actions | CI: линтинг, тесты, сборка образов, e2e-тест |

## Быстрый старт

Нужен только **Docker Desktop** (Windows/macOS) или Docker Engine + Compose v2 (Linux).

```bash
docker compose up -d --build --wait
```

Первая сборка занимает 2–4 минуты. После запуска:

* http://localhost:8080 — веб-клиент;
* http://localhost:8080/api/docs — документация REST API (Swagger UI);
* http://localhost:8222 — мониторинг NATS.

Демо-учётные записи (пароль у всех `demo12345`), на экране входа есть кнопки быстрого входа:

| Роль | Email |
|---|---|
| Клиент | `client@foodflow.local` |
| Ресторан | `mario@foodflow.local`, `sakura@foodflow.local`, `grill@foodflow.local`, `tbilisi@foodflow.local` |
| Курьер | `courier@foodflow.local`, `courier2@foodflow.local` |
| Администратор | `admin@foodflow.local` |

**Демонстрационный сценарий:** курьер выходит на смену → клиент оформляет заказ →
через ~1,5 с заказ оплачен → ресторан принимает заказ (курьер назначается
автоматически) → ресторан отмечает «Готов» → курьер «Забрал заказ» → «Доставил».
Статусы и уведомления обновляются в реальном времени у всех участников.
Заказ дороже 10 000 ₽ демонстрирует отказ в оплате, а отклонение рестораном
оплаченного заказа — компенсацию (возврат средств).

Остановка: `docker compose down` (с удалением данных — `docker compose down -v`).

## Архитектура

```
Веб-клиент (SPA) ──REST/SSE──▶ API Gateway ──gRPC──▶ auth · restaurant · order · payment · delivery · notification
                                    ▲                            │            │          │           │
                                    └── core NATS (push) ◀───────┴──── NATS JetStream (события) ─────┘
                                                                 PostgreSQL: отдельная БД на сервис
```

| Сервис | Порт | Ответственность |
|---|---|---|
| gateway | 8080 (HTTP) | REST API, JWT, RBAC, rate limiting, circuit breaker, композиция ответов, SSE, веб-клиент |
| auth | 9001 | пользователи, роли, выдача JWT |
| restaurant | 9002 | рестораны, меню, цены |
| order | 9003 | оформление заказа, конечный автомат статусов |
| payment | 9004 | эмуляция платёжного провайдера, оплаты и возвраты |
| delivery | 9005 | курьеры, автоматическое назначение, выполнение доставки |
| notification | 9006 | уведомления по событиям, push через NATS |

Подробности — [docs/architecture.md](docs/architecture.md): статусная модель заказа,
каталог событий, паттерны надёжности (Transactional Outbox, Idempotent Consumer,
Saga), безопасность, конфигурация. Диаграммы C4/UML/ER — в `docs/diagrams/`.

## Структура репозитория

```
cmd/<service>/          точки входа (composition root)
internal/<service>/     domain → service → repository, transport/grpcserver, transport/consumer
internal/gateway/       HTTP-слой шлюза, middleware, клиенты gRPC, SSE
pkg/                    общая инфраструктура: grpcx, natsbus, outbox, postgres, migrate, auth, events…
proto/, gen/            контракты gRPC и сгенерированный код
api/openapi.yaml        контракт REST API
web/static/             веб-клиент (HTML/CSS/JS без сборки, встраивается в gateway)
deployments/            Dockerfile, инициализация PostgreSQL
test/e2e/               сквозные тесты через REST API
tools/loadtest/         нагрузочный тест
.github/workflows/      CI-пайплайн GitHub Actions
```

## Разработка

```bash
go test -race -cover ./...                       # unit-тесты
go test -tags e2e -count=1 -v ./test/e2e/...     # e2e (при запущенной системе)
go run ./tools/loadtest -c 50 -d 20s -scenario catalog   # нагрузочный тест
buf lint && buf generate                         # перегенерация gRPC-кода
```

Для нагрузочного теста увеличьте лимит запросов шлюза:
`RATE_LIMIT_RPS=100000 RATE_LIMIT_BURST=100000 docker compose up -d gateway`.

Переменные окружения — в `.env.example` (скопируйте в `.env`, чтобы изменить).
