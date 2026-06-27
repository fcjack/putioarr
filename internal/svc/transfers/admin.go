package transfers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDownloadDirNotConfigured is returned when an admin action needs DOWNLOAD_DIR but it is unset.
var ErrDownloadDirNotConfigured = errors.New("download directory not configured")

// ResetDB wipes all transfer tracking rows from SQLite to recover from bad state.
func (s *Service) ResetDB(_ context.Context) error {
	if err := s.repo.ResetDownloads(); err != nil {
		return fmt.Errorf("failed to reset database: %w", err)
	}

	return nil
}

// PurgeDownloads deletes the contents of DOWNLOAD_DIR. The directory itself is kept.
func (s *Service) PurgeDownloads(_ context.Context) error {
	if s.downloadDir == "" {
		return ErrDownloadDirNotConfigured
	}

	entries, err := os.ReadDir(s.downloadDir)
	if err != nil {
		return fmt.Errorf("failed to read download directory: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(s.downloadDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	return nil
}
