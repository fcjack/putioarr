package sqlite

import (
	"database/sql"
	"time"

	"github.com/italolelis/seedbox_downloader/internal/storage"
)

type DownloadRepository struct {
	db *sql.DB
}

func NewDownloadRepository(dbConn *sql.DB) *DownloadRepository {
	return &DownloadRepository{db: dbConn}
}

func (r *DownloadRepository) GetDownloads() ([]storage.DownloadRecord, error) {
	rows, err := r.db.Query(`SELECT transfer_id, name, downloaded_at, status, locked_by FROM downloads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []storage.DownloadRecord

	for rows.Next() {
		var record storage.DownloadRecord

		var name, lockedBy sql.NullString

		err := rows.Scan(&record.DownloadID, &name, &record.DownloadedAt, &record.Status, &lockedBy)
		if err != nil {
			return nil, err
		}

		record.Name = name.String
		record.LockedBy = lockedBy.String

		downloads = append(downloads, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return downloads, nil
}

// GetByTransferID returns the download record for a single transfer ID.
// It returns storage.ErrNotFound when no row exists for the transfer.
func (r *DownloadRepository) GetByTransferID(transferID string) (storage.DownloadRecord, error) {
	var record storage.DownloadRecord

	var name, lockedBy sql.NullString

	err := r.db.
		QueryRow(`SELECT transfer_id, name, downloaded_at, status, locked_by FROM downloads WHERE transfer_id = ?`, transferID).
		Scan(&record.DownloadID, &name, &record.DownloadedAt, &record.Status, &lockedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return storage.DownloadRecord{}, storage.ErrNotFound
		}

		return storage.DownloadRecord{}, err
	}

	record.Name = name.String
	record.LockedBy = lockedBy.String

	return record, nil
}

// ClaimTransfer atomically sets status to 'downloading' and locked_by to instanceID if status is 'pending' or 'failed'.
// The human-readable name is persisted so the transfer can still be identified in the UI after it is cleaned up from Put.io.
func (r *DownloadRepository) ClaimTransfer(transferID, name string) (bool, error) {
	var status string

	err := r.db.QueryRow(`SELECT status FROM downloads WHERE transfer_id = ?`, transferID).Scan(&status)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}

	if status == "downloaded" {
		return false, storage.ErrDownloaded
	}

	// Now do the upsert/claim. excluded.name backfills the name on rows created
	// before the column existed (or claimed without one).
	rows, err := r.db.Exec(`
		INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by, name)
		VALUES (?, ?, 'downloading', ?, ?)
		ON CONFLICT(transfer_id) DO UPDATE SET
			status = 'downloading',
			locked_by = excluded.locked_by,
			name = COALESCE(NULLIF(downloads.name, ''), excluded.name)
		WHERE downloads.status IN ('pending', 'failed') AND (downloads.locked_by IS NULL OR downloads.locked_by = '')
	`, transferID, time.Now().Format(time.RFC3339), storage.GenerateInstanceID(), name)
	if err != nil {
		return false, err
	}

	affected, _ := rows.RowsAffected()

	return affected > 0, nil
}

// UpdateTransferStatus sets the status for a download.
func (r *DownloadRepository) UpdateTransferStatus(transferID, status string) error {
	_, err := r.db.Exec(`UPDATE downloads SET status = ?, locked_by = NULL WHERE transfer_id = ?`, status, transferID)

	return err
}

// DeleteByTransferID removes a single download record. Deleting a non-existent row is not an error.
func (r *DownloadRepository) DeleteByTransferID(transferID string) error {
	_, err := r.db.Exec(`DELETE FROM downloads WHERE transfer_id = ?`, transferID)

	return err
}

// ResetDownloads wipes the entire downloads table to recover from corrupted tracking state.
func (r *DownloadRepository) ResetDownloads() error {
	_, err := r.db.Exec(`DELETE FROM downloads`)

	return err
}
