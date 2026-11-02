// Payment Service — проведение оплаты заказов и возвратов (эмуляция провайдера).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	"github.com/thaperfectcell/food-delivery/internal/payment/migrations"
	"github.com/thaperfectcell/food-delivery/internal/payment/provider"
	"github.com/thaperfectcell/food-delivery/internal/payment/repository"
	"github.com/thaperfectcell/food-delivery/internal/payment/service"
	"github.com/thaperfectcell/food-delivery/internal/payment/transport/consumer"
	"github.com/thaperfectcell/food-delivery/internal/payment/transport/grpcserver"
	"github.com/thaperfectcell/food-delivery/pkg/config"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/migrate"
	"github.com/thaperfectcell/food-delivery/pkg/natsbus"
	"github.com/thaperfectcell/food-delivery/pkg/outbox"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

type cfg struct {
	LogLevel     string
	GRPCAddr     string
	DatabaseURL  string
	NatsURL      string
	PaymentDelay time.Duration
	MaxAmount    int64
}

func loadConfig() cfg {
	return cfg{
		LogLevel:     config.String("LOG_LEVEL", "info"),
		GRPCAddr:     config.String("GRPC_ADDR", ":9004"),
		DatabaseURL:  config.String("DATABASE_URL", "postgres://payment:payment@localhost:5432/payment?sslmode=disable"),
		NatsURL:      config.String("NATS_URL", "nats://localhost:4222"),
		PaymentDelay: config.Duration("PAYMENT_DELAY", 1500*time.Millisecond),
		MaxAmount:    config.Int64("PAYMENT_MAX_AMOUNT", 1_000_000), // 10 000 ₽
	}
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check service health and exit")
	flag.Parse()
	c := loadConfig()
	if *healthcheck {
		if err := grpcx.CheckHealth(c.GRPCAddr); err != nil {
			os.Exit(1)
		}
		return
	}

	log := logger.New("payment", c.LogLevel)
	if err := run(c, log); err != nil {
		log.Error("service stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(c cfg, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Инфраструктура: БД (+миграции) и брокер сообщений.
	pool, err := postgres.Connect(ctx, c.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Up(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	bus, err := natsbus.Connect(c.NatsURL, "payment", log)
	if err != nil {
		return err
	}
	defer bus.Close()
	if err := bus.EnsureStreams(ctx); err != nil {
		return err
	}

	// Сборка слоёв: repository + provider → service → transport.
	svc := service.New(repository.New(pool), provider.NewSimulator(c.PaymentDelay, c.MaxAmount), log)

	go outbox.NewRelay(pool, bus, log).Run(ctx)

	stopConsumer, err := consumer.New(svc).Subscribe(ctx, bus)
	if err != nil {
		return err
	}
	defer stopConsumer()

	srv := grpcx.NewServer(log)
	paymentv1.RegisterPaymentServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	log.Info("payment provider emulator configured",
		slog.Duration("delay", c.PaymentDelay), slog.Int64("max_amount", c.MaxAmount))
	return srv.Run(ctx, c.GRPCAddr)
}
