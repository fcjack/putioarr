package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepEmptyDirs(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()

	oldEmpty := filepath.Join(downloadDir, "old-empty")
	newEmpty := filepath.Join(downloadDir, "new-empty")
	nonEmpty := filepath.Join(downloadDir, "non-empty")

	for _, dir := range []string{oldEmpty, newEmpty, nonEmpty} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	writeFile(t, filepath.Join(nonEmpty, "keep.mkv"))

	// Age the old-empty dir past the min-age threshold.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldEmpty, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	d := newTestDownloader(t, downloadDir, CleanupOptions{
		SweepInterval: time.Hour,
		SweepMinAge:   24 * time.Hour,
	})

	d.sweepEmptyDirs(context.Background())

	if _, err := os.Stat(oldEmpty); !os.IsNotExist(err) {
		t.Fatal("expected old empty directory to be swept")
	}

	if _, err := os.Stat(newEmpty); err != nil {
		t.Fatalf("recent empty directory must be kept: %v", err)
	}

	if _, err := os.Stat(nonEmpty); err != nil {
		t.Fatalf("non-empty directory must be kept: %v", err)
	}

	if _, err := os.Stat(downloadDir); err != nil {
		t.Fatalf("DOWNLOAD_DIR must never be removed: %v", err)
	}
}

func TestStartSweepDisabled(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	d := newTestDownloader(t, downloadDir, CleanupOptions{SweepInterval: 0})

	// Should be a no-op and not panic or block.
	d.StartSweep(context.Background())
}
