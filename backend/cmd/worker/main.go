// ludiskus worker: đẩy outbox sang lunoti (SKIP LOCKED), đồng bộ cache
// Profile/Space, dọn đính kèm mồ côi, đăng ký event-type lên lunoti
// (docs/02 §2.2, 05, 07, 08).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/notify"
	"ludiskus/internal/platform"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	"ludiskus/internal/storage"
)

const (
	pollInterval    = 2 * time.Second
	cleanupInterval = time.Hour
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker exited", "err", err)
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
		log.Warn("MinIO không khởi tạo được", "err", err)
		store = nil
	}

	repo := repository.New(pool)
	ident := identity.New(repo, rdb, cfg, log)
	lunoti := notify.New(cfg)
	svc := service.New(repo, ident, store, lunoti, markdown.New(), cfg, rdb)

	log.Info("ludiskus worker started", "poll", pollInterval.String(),
		"cacheSync", cfg.ProfileSyncInterval.String(), "hipcoreClient", ident.Enabled(),
		"lunoti", lunoti.Enabled())

	// Đăng ký event-type lên lunoti một lần lúc khởi động (best-effort).
	go svc.RegisterEventTypes(ctx, log)
	go svc.RegisterHiptTasks(ctx)
	go cacheSync(ctx, log, svc, cfg.ProfileSyncInterval)
	go cleanup(ctx, log, svc)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopping")
			return nil
		case <-ticker.C:
			svc.ProcessOutbox(ctx, log)
		}
	}
}

func cacheSync(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) {
	if interval <= 0 {
		return
	}
	svc.SyncCaches(ctx, log)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			svc.SyncCaches(ctx, log)
		}
	}
}

func cleanup(ctx context.Context, log *slog.Logger, svc *service.Service) {
	t := time.NewTicker(cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			svc.CleanupOrphans(ctx, log)
		}
	}
}
