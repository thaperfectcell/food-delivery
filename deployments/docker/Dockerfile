# Единый многоэтапный Dockerfile для всех сервисов монорепозитория.
# Сборка конкретного сервиса: docker build --build-arg SERVICE=order -f deployments/docker/Dockerfile .

# ---------- этап 1: сборка статического бинарника ----------
FROM golang:1.24-alpine AS build
WORKDIR /src
# Сначала только зависимости — слой кэшируется, пока не изменился go.mod/go.sum.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG SERVICE
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test -n "$SERVICE" && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

# ---------- этап 2: минимальный образ выполнения ----------
# distroless/static: нет shell и пакетного менеджера, запуск от непривилегированного пользователя.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
# Проверка здоровья выполняется самим бинарником (флаг -healthcheck).
HEALTHCHECK --interval=10s --timeout=4s --start-period=20s --retries=5 CMD ["/app", "-healthcheck"]
ENTRYPOINT ["/app"]
