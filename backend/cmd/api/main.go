// ludiskus API server (docs/10).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ludiskus/internal/auth"
	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/notify"
	"ludiskus/internal/platform"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	"ludiskus/internal/storage"
	transport "ludiskus/internal/transport/http"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DBDSN, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, log); err != nil {
		return err
	}

	rdb, err := platform.ConnectRedis(ctx, cfg.RedisURL)
	if err != nil {
		log.Warn("redis không sẵn sàng — cache chỉ dùng Postgres", "err", err)
		rdb = nil
	} else {
		defer rdb.Close()
	}

	store, err := storage.New(cfg)
	if err != nil {
		log.Warn("MinIO không khởi tạo được — đính kèm bị vô hiệu", "err", err)
		store = nil
	}

	repo := repository.New(pool)
	ident := identity.New(repo, rdb, cfg, log)
	lunoti := notify.New(cfg)
	svc := service.New(repo, ident, store, lunoti, markdown.New(), cfg, rdb)

	if err := svc.EnsureStorage(ctx); err != nil {
		log.Warn("tạo bucket thất bại", "err", err)
	}
	if !ident.Enabled() {
		log.Warn("LUDISKUS_HIPCORE_CLIENT_ID/SECRET chưa cấu hình — đọc Profile/Space sẽ bị từ chối")
	}

	jwks := auth.NewJWKS(cfg.HipcoreJWKSURL)
	authn := auth.NewAuthenticator(jwks, cfg.HipcoreAudience, cfg.HipcoreURL, log).WithGateway(cfg.GatewaySecret)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           transport.NewRouter(svc, authn, log),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("ludiskus api listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
