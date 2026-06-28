package downloader

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"time"

	"github.com/italolelis/seedbox_downloader/internal/logctx"
)

// StartSweep launches a background job that periodically prunes leftover empty
// directories under DOWNLOAD_DIR. It is a safety net for orphans left behind by
// crashes or partial imports; per-release pruning still happens inline at import time.
// A non-positive SweepInterval disables the job.
func (d *Downloader) StartSweep(ctx context.Context) {
	logger := logctx.LoggerFromContext(ctx)

	if d.cleanup.SweepInterval <= 0 {
		logger.InfoContext(ctx, "periodic cleanup sweep disabled", "operation", "cleanup_sweep")

		return
	}

	logger.InfoContext(ctx, "starting periodic cleanup sweep",
		"operation", "cleanup_sweep",
		"interval", d.cleanup.SweepInterval.String(),
		"min_age", d.cleanup.SweepMinAge.String())

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "cleanup sweep panic",
					"operation", "cleanup_sweep",
					"panic", r,
					"stack", string(debug.Stack()))

				if ctx.Err() == nil {
					time.Sleep(time.Second)
					d.StartSweep(ctx)
				}
			}
		}()

		ticker := time.NewTicker(d.cleanup.SweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "cleanup sweep shutdown",
					"operation", "cleanup_sweep",
					"reason", "context_cancelled")

				return
			case <-ticker.C:
				d.sweepEmptyDirs(ctx)
			}
		}
	}()
}

// sweepEmptyDirs removes empty directories under DOWNLOAD_DIR whose modification time
// is older than SweepMinAge. Children are processed before parents so nested empty
// trees collapse over successive runs. DOWNLOAD_DIR itself is never removed.
func (d *Downloader) sweepEmptyDirs(ctx context.Context) {
	logger := logctx.LoggerFromContext(ctx)

	root := filepath.Clean(d.downloadDir)
	cutoff := time.Now().Add(-d.cleanup.SweepMinAge)

	var dirs []string

	err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			logger.WarnContext(ctx, "cleanup sweep walk error", "operation", "cleanup_sweep", "path", path, "err", err)

			return nil
		}

		if de.IsDir() && filepath.Clean(path) != root {
			dirs = append(dirs, path)
		}

		return nil
	})
	if err != nil {
		logger.WarnContext(ctx, "cleanup sweep failed to walk download dir", "operation", "cleanup_sweep", "err", err)

		return
	}

	// Deepest paths first so a directory is evaluated after its children.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })

	removed := 0

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			continue
		}

		info, err := os.Stat(dir)
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}

		if err := os.Remove(dir); err != nil {
			logger.WarnContext(ctx, "cleanup sweep failed to remove empty directory",
				"operation", "cleanup_sweep", "dir", dir, "err", err)

			continue
		}

		removed++

		logger.InfoContext(ctx, "cleanup sweep removed empty directory",
			"operation", "cleanup_sweep", "removed_path", dir)
	}

	logger.DebugContext(ctx, "cleanup sweep completed",
		"operation", "cleanup_sweep", "scanned_dirs", len(dirs), "removed_dirs", removed)
}
