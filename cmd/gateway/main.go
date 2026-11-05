// API Gateway — единая точка входа во внешний REST API системы FoodFlow.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/thaperfectcell/food-delivery/api"
	"github.com/thaperfectcell/food-delivery/internal/gateway/clients"
	"github.com/thaperfectcell/food-delivery/internal/gateway/config"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpapi"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
	"github.com/thaperfectcell/food-delivery/internal/gateway/realtime"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/natsbus"
	"github.com/thaperfectcell/food-delivery/web"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check gateway health and exit")
	flag.Parse()
	cfg := config.Load()
	if *healthcheck {
		if err := checkHealth(cfg.HTTPAddr); err != nil {
			os.Exit(1)
		}
		return
	}

	log := logger.New("gateway", cfg.LogLevel)
	if err := run(cfg, log); err != nil {
		log.Error("gateway stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Клиенты внутренних сервисов (соединения ленивые, недоступность сервиса
	// при старте не мешает запуску шлюза).
	pool, err := clients.Dial(cfg.Services, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	// NATS нужен только для SSE-уведомлений: без него REST API продолжает работать.
	var (
		notifier httpapi.NotificationSource
		bus      *natsbus.Bus
	)
	if bus, err = natsbus.Connect(cfg.NatsURL, "gateway", log); err != nil {
		log.Warn("nats is unavailable, realtime notifications disabled", slog.String("error", err.Error()))
	} else {
		defer bus.Close()
		notifier = realtime.NewNATSSource(bus.NC)
	}

	ready := func(ctx context.Context) (bool, map[string]string) {
		checks := pool.Health(ctx)
		switch {
		case bus == nil:
			checks["nats"] = "disabled"
		case bus.NC.IsConnected():
			checks["nats"] = "ok"
		default:
			checks["nats"] = strings.ToLower(bus.NC.Status().String())
		}
		return true, checks
	}

	shutdown := make(chan struct{})
	handler := httpapi.NewHandler(httpapi.Deps{
		Clients:     pool.Set,
		Tokens:      auth.NewTokenManager(cfg.JWTSecret, 24*time.Hour),
		Notifier:    notifier,
		Ready:       ready,
		Log:         log,
		Static:      web.Static(),
		OpenAPI:     api.OpenAPI,
		CallTimeout: cfg.CallTimeout,
		RateLimiter: middleware.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
		CORSOrigins: cfg.CORSOrigins,
		BodyLimit:   cfg.BodyLimit,
		Shutdown:    shutdown,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second, // SSE-поток снимает таймаут для своего соединения
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", slog.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Корректная остановка: закрываем SSE-потоки, дожидаемся завершения
	// активных запросов не дольше 10 секунд.
	log.Info("shutting down")
	close(shutdown)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("http server stopped")
	return nil
}

// checkHealth выполняет GET /healthz (режим -healthcheck для Docker HEALTHCHECK).
func checkHealth(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}
