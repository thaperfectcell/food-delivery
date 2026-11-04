// Package config — конфигурация API Gateway из переменных окружения.
package config

import (
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/config"
)

// Services — адреса внутренних gRPC-сервисов.
type Services struct {
	Auth         string
	Restaurant   string
	Order        string
	Payment      string
	Delivery     string
	Notification string
}

// Config — параметры шлюза.
type Config struct {
	HTTPAddr       string
	LogLevel       string
	JWTSecret      string
	NatsURL        string
	Services       Services
	RateLimitRPS   float64
	RateLimitBurst int
	CORSOrigins    []string
	CallTimeout    time.Duration // таймаут одного gRPC-вызова
	BodyLimit      int64         // максимальный размер тела запроса, байт
}

// Load читает конфигурацию (значения по умолчанию рассчитаны на локальный запуск).
func Load() Config {
	return Config{
		HTTPAddr:  config.String("HTTP_ADDR", ":8080"),
		LogLevel:  config.String("LOG_LEVEL", "info"),
		JWTSecret: config.String("JWT_SECRET", "dev-secret-change-me"),
		NatsURL:   config.String("NATS_URL", "nats://localhost:4222"),
		Services: Services{
			Auth:         config.String("AUTH_GRPC_ADDR", "localhost:9001"),
			Restaurant:   config.String("RESTAURANT_GRPC_ADDR", "localhost:9002"),
			Order:        config.String("ORDER_GRPC_ADDR", "localhost:9003"),
			Payment:      config.String("PAYMENT_GRPC_ADDR", "localhost:9004"),
			Delivery:     config.String("DELIVERY_GRPC_ADDR", "localhost:9005"),
			Notification: config.String("NOTIFICATION_GRPC_ADDR", "localhost:9006"),
		},
		RateLimitRPS:   config.Float("RATE_LIMIT_RPS", 20),
		RateLimitBurst: config.Int("RATE_LIMIT_BURST", 40),
		CORSOrigins:    config.List("CORS_ORIGINS", []string{"*"}),
		CallTimeout:    config.Duration("GRPC_CALL_TIMEOUT", 5*time.Second),
		BodyLimit:      config.Int64("HTTP_BODY_LIMIT", 1<<20),
	}
}
