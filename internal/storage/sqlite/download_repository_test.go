package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
