package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
)

func runUploadCleanupSweeper(ctx context.Context, pool *pgxpool.Pool, store storage.Storage) {
	if store == nil || os.Getenv("UPLOAD_CLEANUP_ENABLED") == "false" {
		return
	}
	retention := envDuration("UPLOAD_CLEANUP_RETENTION", 7*24*time.Hour)
	if retention < 24*time.Hour {
		slog.Warn("upload cleanup retention below minimum; using seven days")
		retention = 7 * 24 * time.Hour
	}
	interval := max(envDuration("UPLOAD_CLEANUP_INTERVAL", time.Hour), time.Minute)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	cleaner := service.UploadCleaner{Pool: pool, Storage: store}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			report, err := cleaner.Sweep(sweepCtx, service.UploadCleanupOptions{
				Cutoff: now.Add(-retention), Limit: 100, Apply: os.Getenv("UPLOAD_CLEANUP_DRY_RUN") != "true",
			})
			cancel()
			if err != nil {
				slog.Warn("upload cleanup round failed; will retry", "error", err, "deleted", report.Deleted)
			} else if len(report.Candidates) > 0 {
				slog.Info("upload cleanup round completed", "candidates", len(report.Candidates),
					"candidate_bytes", report.CandidateBytes, "deleted", report.Deleted, "deleted_bytes", report.DeletedBytes)
			}
		}
	}
}
