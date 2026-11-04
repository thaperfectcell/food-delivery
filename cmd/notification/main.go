// Notification Service — уведомления пользователей по доменным событиям системы.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	notificationv1 "github.com/thaperfectcell/food-delivery/gen/notification/v1"
	"github.com/thaperfectcell/food-delivery/internal/notification/migrations"
	"github.com/thaperfectcell/food-delivery/internal/notification/push"
	"github.com/thaperfectcell/food-delivery/internal/notification/repository"
	"github.com/thaperfectcell/food-delivery/internal/notification/service"
	"github.com/thaperfectcell/food-delivery/internal/notification/transport/consumer"
	"github.com/thaperfectcell/food-delivery/internal/notification/transport/grpcserver"
	"github.com/thaperfectcell/food-delivery/pkg/config"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/migrate"
	"github.com/thaperfectcell/food-delivery/pkg/natsbus"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

type cfg struct {
	LogLevel    string
	GRPCAddr    string
	DatabaseURL string
	NatsURL     string
}

func loadConfig() cfg {
	return cfg{
		LogLevel:    config.String("LOG_LEVEL", "info"),
		GRPCAddr:    config.String("GRPC_ADDR", ":9006"),
		DatabaseURL: config.String("DATABASE_URL", "postgres://notification:notification@localhost:5432/notification?sslmode=disable"),
		NatsURL:     config.String("NATS_URL", "nats://localhost:4222"),
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

	log := logger.New("notification", c.LogLevel)
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

	bus, err := natsbus.Connect(c.NatsURL, "notification", log)
	if err != nil {
		return err
	}
	defer bus.Close()
	if err := bus.EnsureStreams(ctx); err != nil {
		return err
	}

	// Сборка слоёв: repository + push → service → transport.
	svc := service.New(repository.New(pool), push.New(bus.NC), log)

	stopConsumers, err := consumer.New(svc).Subscribe(ctx, bus)
	if err != nil {
		return err
	}
	defer stopConsumers()

	srv := grpcx.NewServer(log)
	notificationv1.RegisterNotificationServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	return srv.Run(ctx, c.GRPCAddr)
}
