// Restaurant Service — каталог ресторанов и меню.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/migrations"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/repository"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/service"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/transport/grpcserver"
	"github.com/thaperfectcell/food-delivery/pkg/config"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/migrate"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

type cfg struct {
	LogLevel    string
	GRPCAddr    string
	DatabaseURL string
	DemoSeed    bool
}

func loadConfig() cfg {
	return cfg{
		LogLevel:    config.String("LOG_LEVEL", "info"),
		GRPCAddr:    config.String("GRPC_ADDR", ":9002"),
		DatabaseURL: config.String("DATABASE_URL", "postgres://restaurant:restaurant@localhost:5432/restaurant?sslmode=disable"),
		DemoSeed:    config.Bool("DEMO_SEED", true),
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

	log := logger.New("restaurant", c.LogLevel)
	if err := run(c, log); err != nil {
		log.Error("service stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(c cfg, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Инфраструктура: БД и миграции схемы.
	pool, err := postgres.Connect(ctx, c.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Up(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	// Сборка слоёв: repository → service → transport.
	svc := service.New(repository.New(pool), log)

	if c.DemoSeed {
		added, err := svc.SeedDemo(ctx)
		if err != nil {
			return err
		}
		log.Info("demo restaurants seeded", slog.Int("added", added))
	}

	srv := grpcx.NewServer(log)
	restaurantv1.RegisterRestaurantServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	return srv.Run(ctx, c.GRPCAddr)
}
