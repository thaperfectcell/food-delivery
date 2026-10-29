// Auth Service — регистрация, аутентификация и выдача JWT-токенов доступа.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	"github.com/thaperfectcell/food-delivery/internal/auth/migrations"
	"github.com/thaperfectcell/food-delivery/internal/auth/repository"
	"github.com/thaperfectcell/food-delivery/internal/auth/security"
	"github.com/thaperfectcell/food-delivery/internal/auth/service"
	"github.com/thaperfectcell/food-delivery/internal/auth/transport/grpcserver"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/config"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/migrate"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

const defaultJWTSecret = "dev-secret-change-me"

type cfg struct {
	LogLevel    string
	GRPCAddr    string
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration
	DemoSeed    bool
}

func loadConfig() cfg {
	return cfg{
		LogLevel:    config.String("LOG_LEVEL", "info"),
		GRPCAddr:    config.String("GRPC_ADDR", ":9001"),
		DatabaseURL: config.String("DATABASE_URL", "postgres://auth:auth@localhost:5432/auth?sslmode=disable"),
		JWTSecret:   config.String("JWT_SECRET", defaultJWTSecret),
		JWTTTL:      config.Duration("JWT_TTL", 24*time.Hour),
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

	log := logger.New("auth", c.LogLevel)
	if err := run(c, log); err != nil {
		log.Error("service stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(c cfg, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if c.JWTSecret == defaultJWTSecret {
		log.Warn("JWT_SECRET is not set, using insecure development secret")
	}

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
	svc := service.New(
		repository.New(pool),
		security.BcryptHasher{},
		auth.NewTokenManager(c.JWTSecret, c.JWTTTL),
		log,
	)

	if c.DemoSeed {
		added, err := svc.SeedDemo(ctx)
		if err != nil {
			return err
		}
		log.Info("demo users seeded", slog.Int("added", added))
	}

	srv := grpcx.NewServer(log)
	authv1.RegisterAuthServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	return srv.Run(ctx, c.GRPCAddr)
}
