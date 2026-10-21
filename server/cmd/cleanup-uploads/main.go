// cleanup-uploads previews or runs the same collector used by the server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cleanup-uploads", flag.ContinueOnError)
	apply := flags.Bool("apply", false, "delete eligible uploads (default: preview only)")
	retention := flags.Duration("retention", 7*24*time.Hour, "retention since owner update/upload; minimum 24h")
	limit := flags.Int("limit", 100, "maximum objects deleted per invocation")
	workspace := flags.String("workspace", "", "optional workspace UUID; references are checked globally")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *retention < 24*time.Hour || *limit < 1 || *limit > 1000 {
		return fmt.Errorf("require retention >= 24h, limit 1..1000, and no positional arguments")
	}
	options := service.UploadCleanupOptions{Apply: *apply, Limit: *limit, Cutoff: time.Now().Add(-*retention)}
	if *workspace != "" {
		id, err := util.ParseUUID(*workspace)
		if err != nil {
			return fmt.Errorf("parse workspace: %w", err)
		}
		options.WorkspaceID = id
	}
	if os.Getenv("DATABASE_URL") == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	var store storage.Storage
	if s3 := storage.NewS3StorageFromEnv(); s3 != nil {
		store = s3
	} else if local := storage.NewLocalStorageFromEnv(); local != nil {
		store = local
	} else {
		return fmt.Errorf("upload storage is unavailable")
	}
	report, sweepErr := (service.UploadCleaner{Pool: pool, Storage: store}).Sweep(ctx, options)
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return fmt.Errorf("write cleanup report: %w", err)
	}
	return sweepErr
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 30*time.Second)
	defer timeout()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
