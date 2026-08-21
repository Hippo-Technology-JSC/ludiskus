// ludiskus worker: đẩy outbox sang lunoti (SKIP LOCKED), đồng bộ cache
// Profile/Space, chuyển snapshot interaction lịch sử, dọn đính kèm mồ côi,
// đăng ký event-type lên lunoti
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
	pollInterval             = 2 * time.Second
	cleanupInterval          = time.Hour
	commentNotifyInterval    = 10 * time.Second
	commentVerifyInterval    = 30 * time.Second
	commentReconcileInterval = time.Hour
	commentHardeningInterval = time.Hour
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
	if cfg.CommentEnabled {
		go commentNotify(ctx, log, svc)
		go commentVerify(ctx, log, svc)
		go commentReconcile(ctx, log, svc, cfg.CommentReconcileHour)
		go commentHardening(ctx, log, svc)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopping")
			return nil
		case <-ticker.C:
			svc.ProcessInteractionBackfill(ctx, log)
			svc.ProcessOutbox(ctx, log)
		}
	}
}

func commentHardening(ctx context.Context, log *slog.Logger, svc *service.Service) {
	t := time.NewTicker(commentHardeningInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := svc.DetectCommentAbuse(ctx); err != nil {
				log.Error("detect comment abuse", "err", err)
			} else if n > 0 {
				log.Warn("comment abuse flags raised", "count", n)
			}
			if n, err := svc.SyncCommentScores(ctx); err != nil {
				log.Error("sync comment scores", "err", err)
			} else if n > 0 {
				log.Info("synced comment scores", "count", n)
			}
		}
	}
}

func commentNotify(ctx context.Context, log *slog.Logger, svc *service.Service) {
	t := time.NewTicker(commentNotifyInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := svc.FlushCommentNotify(ctx)
			if err != nil {
				log.Error("flush comment notifications", "err", err)
			} else if n > 0 {
				log.Info("flushed comment notifications", "count", n)
			}
		}
	}
}
func commentVerify(ctx context.Context, log *slog.Logger, svc *service.Service) {
	t := time.NewTicker(commentVerifyInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := svc.VerifyCommentTargets(ctx)
			if err != nil {
				log.Error("verify comment targets", "err", err)
			} else if n > 0 {
				log.Info("verified comment targets", "count", n)
			}
		}
	}
}
func commentReconcile(ctx context.Context, log *slog.Logger, svc *service.Service, hourUTC int) {
	t := time.NewTicker(commentReconcileInterval)
	defer t.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			day := now.UTC().Format("2006-01-02")
			if now.UTC().Hour() != hourUTC || day == last {
				continue
			}
			n, err := svc.ReconcileCommentCounts(ctx, "")
			if err != nil {
				log.Error("reconcile comment counters", "err", err)
			} else {
				last = day
				log.Info("reconciled comment counters", "count", n)
			}
			if swept, e := svc.SweepCommentData(ctx); e != nil {
				log.Error("sweep comment data", "err", e)
			} else if swept > 0 {
				log.Info("swept comment data", "count", swept)
			}
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
