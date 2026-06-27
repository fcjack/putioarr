package transfers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
)

func TestServiceResetDB(t *testing.T) {
	t.Parallel()

	repo := &mockRepo{}
	svc := transfers.NewService(&mockLister{}, repo, &mockRequeuer{}, &mockCanceller{}, &mockPutioRemover{}, "tv-sonarr", "")

	if err := svc.ResetDB(context.Background()); err != nil {
		t.Fatalf("ResetDB returned error: %v", err)
	}

	if !repo.resetHit {
		t.Error("expected ResetDownloads to be called")
	}
}

func TestServicePurgeDownloads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	svc := transfers.NewService(&mockLister{}, &mockRepo{}, &mockRequeuer{}, &mockCanceller{}, &mockPutioRemover{}, "tv-sonarr", dir)

	if err := svc.PurgeDownloads(context.Background()); err != nil {
		t.Fatalf("PurgeDownloads returned error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected empty download dir, got %d entries", len(entries))
	}
}

func TestServicePurgeDownloadsNoDir(t *testing.T) {
	t.Parallel()

	svc := transfers.NewService(&mockLister{}, &mockRepo{}, &mockRequeuer{}, &mockCanceller{}, &mockPutioRemover{}, "tv-sonarr", "")

	if err := svc.PurgeDownloads(context.Background()); err == nil {
		t.Fatal("expected error when download dir not configured")
	}
}
