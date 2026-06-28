package transfers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/italolelis/seedbox_downloader/internal/dc/putio"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

// ErrNoActiveDownload is returned when cancelling a transfer that is not actively downloading.
var ErrNoActiveDownload = errors.New("no active local download for transfer")

// DeleteScopes selects which artifacts of a transfer to remove.
type DeleteScopes struct {
	Putio bool
	Local bool
	DB    bool
}

// Any reports whether at least one scope is selected.
func (d DeleteScopes) Any() bool {
	return d.Putio || d.Local || d.DB
}

// Retry resets a transfer for another local download attempt: it marks the SQLite row
// pending, removes partial local files, and re-enqueues the NAS copy immediately.
func (s *Service) Retry(ctx context.Context, id string) error {
	if err := s.repo.UpdateTransferStatus(id, "pending"); err != nil {
		return fmt.Errorf("failed to reset transfer status: %w", err)
	}

	if err := s.deleteLocalFiles(ctx, id); err != nil {
		return fmt.Errorf("failed to remove partial local files: %w", err)
	}

	if err := s.requeuer.Requeue(ctx, id); err != nil {
		return fmt.Errorf("failed to requeue transfer: %w", err)
	}

	return nil
}

// Cancel stops an in-flight local download and marks the transfer failed.
func (s *Service) Cancel(ctx context.Context, id string) error {
	if !s.canceller.CancelDownload(id) {
		return fmt.Errorf("%w: %s", ErrNoActiveDownload, id)
	}

	if err := s.repo.UpdateTransferStatus(id, "failed"); err != nil {
		return fmt.Errorf("failed to update transfer status: %w", err)
	}

	return nil
}

// Delete removes a transfer from the selected scopes (Put.io, local disk, SQLite).
// Failures across scopes are aggregated so a partial failure is fully reported.
func (s *Service) Delete(ctx context.Context, id string, scopes DeleteScopes) error {
	var errs []error

	if scopes.Putio {
		if err := s.removeFromPutio(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("putio: %w", err))
		}
	}

	if scopes.Local {
		if err := s.deleteLocalFiles(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("local: %w", err))
		}
	}

	if scopes.DB {
		if err := s.repo.DeleteByTransferID(id); err != nil {
			errs = append(errs, fmt.Errorf("db: %w", err))
		}
	}

	return errors.Join(errs...)
}

// removeFromPutio cancels the Put.io transfer and deletes its remote files. Put.io
// removal is keyed by the sha1 hash of the transfer ID (matching the Transmission proxy).
// A transfer that is already gone from Put.io (e.g. an item cleaned up after import) is
// treated as success: deletion is idempotent, the desired end state is already met.
func (s *Service) removeFromPutio(ctx context.Context, id string) error {
	hash := sha1.Sum([]byte(id))
	hashStr := hex.EncodeToString(hash[:])

	if err := s.putioRemover.RemoveTransfers(ctx, []string{hashStr}, true); err != nil {
		if errors.Is(err, putio.ErrTransferNotFound) {
			return nil
		}

		return fmt.Errorf("failed to remove transfer from Put.io: %w", err)
	}

	return nil
}

// deleteLocalFiles removes the transfer's files (and its folder) under DOWNLOAD_DIR.
// When the transfer is no longer resolvable on Put.io, there is nothing to delete.
func (s *Service) deleteLocalFiles(ctx context.Context, id string) error {
	if s.downloadDir == "" {
		return nil
	}

	t, err := s.findTransfer(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}

		return err
	}

	for _, f := range t.Files {
		path := filepath.Join(s.downloadDir, f.Path)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	if t.Name != "" {
		folder := filepath.Join(s.downloadDir, t.Name)
		if err := os.RemoveAll(folder); err != nil {
			return fmt.Errorf("failed to remove folder %s: %w", folder, err)
		}
	}

	return nil
}

func (s *Service) findTransfer(ctx context.Context, id string) (*transfer.Transfer, error) {
	transfers, err := s.lister.GetTaggedTorrents(ctx, s.label)
	if err != nil {
		return nil, fmt.Errorf("failed to list transfers: %w", err)
	}

	for _, t := range transfers {
		if t.ID == id {
			return t, nil
		}
	}

	return nil, ErrNotFound
}
