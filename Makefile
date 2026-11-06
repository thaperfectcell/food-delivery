# Основные команды разработки FoodFlow (на Windows без make — см. README).
.PHONY: up down logs ps test test-e2e lint proto build load

up:            ## собрать и запустить всю систему
	docker compose up -d --build --wait

down:          ## остановить систему и удалить данные
	docker compose down -v

logs:          ## журналы всех сервисов
	docker compose logs -f --tail=100

ps:            ## состояние контейнеров
	docker compose ps

test:          ## unit-тесты с детектором гонок и покрытием
	go test -race -cover ./...

test-e2e:      ## сквозные тесты (система должна быть запущена)
	go test -tags e2e -count=1 -v ./test/e2e/...

lint:          ## форматирование и статический анализ
	gofmt -l . && go vet ./...

proto:         ## перегенерировать Go-код из .proto (buf + плагины)
	buf lint && buf generate

build:         ## собрать бинарники всех сервисов в ./bin
	go build -o bin/ ./cmd/...

load:          ## нагрузочный тест каталога (50 клиентов, 20 с)
	go run ./tools/loadtest -c 50 -d 20s -scenario catalog
