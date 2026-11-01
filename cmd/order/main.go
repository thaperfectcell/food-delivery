// Order Service — оформление заказов и управление их жизненным циклом.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/order/client"
	"github.com/thaperfectcell/food-delivery/internal/order/migrations"
	"github.com/thaperfectcell/food-delivery/internal/order/repository"
	"github.com/thaperfectcell/food-delivery/internal/order/service"
	"github.com/thaperfectcell/food-delivery/internal/order/transport/consumer"
	"github.com/thaperfectcell/food-delivery/internal/order/transport/grpcserver"
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
	RestaurantAddr string
}

func loadConfig() cfg {
	return cfg{
		LogLevel:       config.String("LOG_LEVEL", "info"),
		GRPCAddr:       config.String("GRPC_ADDR", ":9003"),
		DatabaseURL:    config.String("DATABASE_URL", "postgres://orders:orders@localhost:5432/orders?sslmode=disable"),
		NatsURL:        config.String("NATS_URL", "nats://localhost:4222"),
		RestaurantAddr: config.String("RESTAURANT_GRPC_ADDR", "localhost:9002"),
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

	log := logger.New("order", c.LogLevel)
	if err := run(c, log); err != nil {
		log.Error("service stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(c cfg, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Инфраструктура: БД (+миграции), брокер, клиенты других сервисов.
	pool, err := postgres.Connect(ctx, c.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Up(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	bus, err := natsbus.Connect(c.NatsURL, "order", log)
	if err != nil {
		return err
	}
	defer bus.Close()
	if err := bus.EnsureStreams(ctx); err != nil {
		return err
	}

	restaurantConn, err := grpcx.Dial(c.RestaurantAddr)
	if err != nil {
		return err
	}
	defer restaurantConn.Close()

	// Сборка слоёв: repository → service → transport.
	repo := repository.New(pool)
	svc := service.New(repo, client.NewCatalog(restaurantv1.NewRestaurantServiceClient(restaurantConn)), log)

	go outbox.NewRelay(pool, bus, log).Run(ctx)

	stopConsumers, err := consumer.New(svc).Subscribe(ctx, bus)
	if err != nil {
		return err
	}
	defer stopConsumers()

	srv := grpcx.NewServer(log)
	orderv1.RegisterOrderServiceServer(srv, grpcserver.New(svc))
	srv.SetServing()
	return srv.Run(ctx, c.GRPCAddr)
}
