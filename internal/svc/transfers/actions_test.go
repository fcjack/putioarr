package transfers_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/dc/putio"
	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

func TestServiceRetry(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{{ID: "1", Name: "Show", Status: "completed"}}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{"1": {DownloadID: "1", Status: "downloaded"}}}

	svc, requeuer, _, _ := newService(lister, repo)

	if err := svc.Retry(context.Background(), "1"); err != nil {
		t.Fatalf("Retry returned error: %v", err)
	}

	if repo.statuses["1"] != "pending" {
		t.Errorf("expected status pending, got %q", repo.statuses["1"])
	}

	if len(requeuer.requeued) != 1 || requeuer.requeued[0] != "1" {
		t.Errorf("expected requeue of transfer 1, got %v", requeuer.requeued)
	}
}

func TestServiceCancel(t *testing.T) {
	t.Parallel()

	svc, _, canceller, _ := newService(&mockLister{}, &mockRepo{})

	if err := svc.Cancel(context.Background(), "1"); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	if len(canceller.cancelled) != 1 {
		t.Errorf("expected one cancellation, got %v", canceller.cancelled)
	}
}

func TestServiceCancelNoActiveDownload(t *testing.T) {
	t.Parallel()

	requeuer := &mockRequeuer{}
	canceller := &mockCanceller{fail: true}
	remover := &mockPutioRemover{}
	svc := transfers.NewService(&mockLister{}, &mockRepo{}, requeuer, canceller, remover, "tv-sonarr", "")

	err := svc.Cancel(context.Background(), "1")
	if !errors.Is(err, transfers.ErrNoActiveDownload) {
		t.Fatalf("expected ErrNoActiveDownload, got %v", err)
	}
}

func TestServiceDeleteScopes(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{{ID: "7", Name: "Show", Status: "completed"}}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{"7": {DownloadID: "7", Status: "failed"}}}

	svc, _, _, remover := newService(lister, repo)

	scopes := transfers.DeleteScopes{Putio: true, DB: true}
	if err := svc.Delete(context.Background(), "7", scopes); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if len(remover.removed) != 1 {
		t.Fatalf("expected one Put.io removal, got %v", remover.removed)
	}

	expectedHash := sha1Hex("7")
	if remover.removed[0][0] != expectedHash {
		t.Errorf("expected Put.io removal by hash %q, got %q", expectedHash, remover.removed[0][0])
	}

	if !remover.deleteData {
		t.Error("expected Put.io removal to delete remote data")
	}

	if len(repo.deleted) != 1 || repo.deleted[0] != "7" {
		t.Errorf("expected DB delete of 7, got %v", repo.deleted)
	}
}

// TestServiceDeleteAlreadyCleanedUp verifies that deleting an item already removed
// from Put.io (e.g. a cleaned-up transfer) succeeds instead of surfacing an error.
func TestServiceDeleteAlreadyCleanedUp(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{"7": {DownloadID: "7", Name: "Show", Status: "cleaned_up"}}}

	svc, _, _, remover := newService(lister, repo)
	remover.returnError = fmt.Errorf("transfer not found: [hash]: %w", putio.ErrTransferNotFound)

	scopes := transfers.DeleteScopes{Putio: true, DB: true}
	if err := svc.Delete(context.Background(), "7", scopes); err != nil {
		t.Fatalf("Delete should treat an already-removed Put.io transfer as success, got: %v", err)
	}

	if len(repo.deleted) != 1 || repo.deleted[0] != "7" {
		t.Errorf("expected DB delete of 7 to still run, got %v", repo.deleted)
	}
}

func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s))

	return hex.EncodeToString(h[:])
}
