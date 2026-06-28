package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/stretchr/testify/require"
)

func newTestRepo(t *testing.T) *DownloadRepository {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "downloads.db")

	db, err := InitDB(ctx, dbPath, 1, 1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return NewDownloadRepository(db)
}

func TestGetDownloads_ScansTableColumns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "downloads.db")

	db, err := InitDB(ctx, dbPath, 1, 1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewDownloadRepository(db)

	_, err = db.Exec(
		`INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by) VALUES (?, ?, ?, ?)`,
		"42", "2026-06-26T12:00:00Z", "downloading", "instance-1",
	)
	require.NoError(t, err)

	records, err := repo.GetDownloads()
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "42", records[0].DownloadID)
	require.Equal(t, "downloading", records[0].Status)
	require.Equal(t, "instance-1", records[0].LockedBy)
}

func TestClaimTransferPersistsName(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	claimed, err := repo.ClaimTransfer("42", "The Matrix (1999)")
	require.NoError(t, err)
	require.True(t, claimed)

	record, err := repo.GetByTransferID("42")
	require.NoError(t, err)
	require.Equal(t, "The Matrix (1999)", record.Name)

	records, err := repo.GetDownloads()
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "The Matrix (1999)", records[0].Name)
}

func TestClaimTransferBackfillsLegacyName(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	// Simulate a legacy row created before the name column was populated.
	_, err := repo.db.Exec(
		`INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by, name) VALUES (?, ?, 'failed', NULL, NULL)`,
		"99", "2026-06-26T12:00:00Z",
	)
	require.NoError(t, err)

	claimed, err := repo.ClaimTransfer("99", "Backfilled Name")
	require.NoError(t, err)
	require.True(t, claimed)

	record, err := repo.GetByTransferID("99")
	require.NoError(t, err)
	require.Equal(t, "Backfilled Name", record.Name)
}

func TestGetByTransferID(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	_, err := repo.db.Exec(
		`INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by) VALUES (?, ?, ?, ?)`,
		"7", "2026-06-26T12:00:00Z", "downloaded", nil,
	)
	require.NoError(t, err)

	record, err := repo.GetByTransferID("7")
	require.NoError(t, err)
	require.Equal(t, "7", record.DownloadID)
	require.Equal(t, "downloaded", record.Status)
	require.Empty(t, record.LockedBy)

	_, err = repo.GetByTransferID("missing")
	require.True(t, errors.Is(err, storage.ErrNotFound))
}

func TestDeleteByTransferID(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	_, err := repo.db.Exec(
		`INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by) VALUES (?, ?, ?, ?)`,
		"7", "2026-06-26T12:00:00Z", "downloaded", nil,
	)
	require.NoError(t, err)

	require.NoError(t, repo.DeleteByTransferID("7"))

	_, err = repo.GetByTransferID("7")
	require.True(t, errors.Is(err, storage.ErrNotFound))

	// Deleting a non-existent row is not an error.
	require.NoError(t, repo.DeleteByTransferID("does-not-exist"))
}

func TestResetDownloads(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	for _, id := range []string{"1", "2", "3"} {
		_, err := repo.db.Exec(
			`INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by) VALUES (?, ?, ?, ?)`,
			id, "2026-06-26T12:00:00Z", "downloaded", nil,
		)
		require.NoError(t, err)
	}

	require.NoError(t, repo.ResetDownloads())

	records, err := repo.GetDownloads()
	require.NoError(t, err)
	require.Empty(t, records)
}
