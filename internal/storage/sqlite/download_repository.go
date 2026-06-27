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
	rows, err := r.db.Query(`SELECT transfer_id, downloaded_at, status, locked_by FROM downloads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var downloads []storage.DownloadRecord

	for rows.Next() {
		var record storage.DownloadRecord

		var lockedBy sql.NullString

		err := rows.Scan(&record.DownloadID, &record.DownloadedAt, &record.Status, &lockedBy)
		if err != nil {
			return nil, err
		}

		record.LockedBy = ""
		if lockedBy.Valid {
			record.LockedBy = lockedBy.String
		}

		downloads = append(downloads, record)
	}

	return downloads, nil
}

// GetByTransferID returns the download record for a single transfer ID.
// It returns storage.ErrNotFound when no row exists for the transfer.
func (r *DownloadRepository) GetByTransferID(transferID string) (storage.DownloadRecord, error) {
	var record storage.DownloadRecord

	var lockedBy sql.NullString

	err := r.db.
		QueryRow(`SELECT transfer_id, downloaded_at, status, locked_by FROM downloads WHERE transfer_id = ?`, transferID).
		Scan(&record.DownloadID, &record.DownloadedAt, &record.Status, &lockedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return storage.DownloadRecord{}, storage.ErrNotFound
		}

		return storage.DownloadRecord{}, err
	}

	record.LockedBy = ""
	if lockedBy.Valid {
		record.LockedBy = lockedBy.String
	}

	return record, nil
}

// ClaimTransfer atomically sets status to 'downloading' and locked_by to instanceID if status is 'pending' or 'failed'.
func (r *DownloadRepository) ClaimTransfer(transferID string) (bool, error) {
	var status string

	err := r.db.QueryRow(`SELECT status FROM downloads WHERE transfer_id = ?`, transferID).Scan(&status)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}

	if status == "downloaded" {
		return false, storage.ErrDownloaded
	}

	// Now do the upsert/claim
	rows, err := r.db.Exec(`
		INSERT INTO downloads (transfer_id, downloaded_at, status, locked_by)
		VALUES (?, ?, 'downloading', ?)
		ON CONFLICT(transfer_id) DO UPDATE SET
			status = 'downloading',
			locked_by = excluded.locked_by
		WHERE downloads.status IN ('pending', 'failed') AND (downloads.locked_by IS NULL OR downloads.locked_by = '')
	`, transferID, time.Now().Format(time.RFC3339), storage.GenerateInstanceID())
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
