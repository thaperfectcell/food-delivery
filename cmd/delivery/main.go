// Delivery Service — курьеры, автоматическое назначение и выполнение доставок.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	"github.com/thaperfectcell/food-delivery/internal/delivery/migrations"
	"github.com/thaperfectcell/food-delivery/internal/delivery/repository"
	"github.com/thaperfectcell/food-delivery/internal/delivery/service"
	"github.com/thaperfectcell/food-delivery/internal/delivery/transport/consumer"
	"github.com/thaperfectcell/food-delivery/internal/delivery/transport/grpcserver"
	"github.com/thaperfectcell/food-delivery/pkg/config"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/migrate"
	"github.com/thaperfectcell/food-delivery/pkg/natsbus"
	"github.com/thaperfectcell/food-delivery/pkg/outbox"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

type cfg struct {
	LogLevel       string
	GRPCAddr       string
	DatabaseURL    string
	NatsURL        string
	AssignInterval time.Duration
}

func loadConfig() cfg {
	return cfg{
		LogLevel:       config.String("LOG_LEVEL", "info"),
		GRPCAddr:       config.String("GRPC_ADDR", ":9005"),
		DatabaseURL:    config.String("DATABASE_URL", "postgres://delivery:delivery@localhost:5432/delivery?sslmode=disable"),
		NatsURL:        config.String("NATS_URL", "nats://localhost:4222"),
		AssignInterval: config.Duration("ASSIGN_INTERVAL", 5*time.Second),
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

	log := logger.New("delivery", c.LogLevel)
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

	bus, err := natsbus.Connect(c.NatsURL, "delivery", log)
	if err != nil {
		return err
	}
	defer bus.Close()
	if err := bus.EnsureStreams(ctx); err != nil {
		return err
	}

	// Сборка слоёв: repository → service → transport.
	svc := service.New(repository.New(pool), log)

	go outbox.NewRelay(pool, bus, log).Run(ctx)
	go svc.RunAssignmentLoop(ctx, c.AssignInterval)

	stopConsumer, err := consumer.New(svc).Subscribe(ctx, bus)
	if err != nil {
		return err
	}
	defer stopConsumer()

	srv := grpcx.NewServer(log)
	deliveryv1.RegisterDeliveryServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	return srv.Run(ctx, c.GRPCAddr)
}
