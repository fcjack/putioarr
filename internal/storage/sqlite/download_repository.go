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

// AssociateTorrentHash maps Transmission's torrent hash to a Put.io transfer ID.
func (r *DownloadRepository) AssociateTorrentHash(hash, transferID string) error {
	_, err := r.db.Exec(`
		INSERT INTO torrent_seed_criteria (hash, transfer_id) VALUES (?, ?)
		ON CONFLICT(hash) DO UPDATE SET transfer_id = excluded.transfer_id
	`, hash, transferID)

	return err
}

// UpdateTorrentSeedCriteria persists per-torrent Transmission limits.
// Nil fields leave previously stored values unchanged.
func (r *DownloadRepository) UpdateTorrentSeedCriteria(hash string, criteria storage.TorrentSeedCriteria) error {
	_, err := r.db.Exec(`
		INSERT INTO torrent_seed_criteria (hash, transfer_id, seed_ratio_limit, seed_ratio_mode, seed_idle_limit, seed_idle_mode, stopped)
		VALUES (?, NULL, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			stopped = COALESCE(excluded.stopped, torrent_seed_criteria.stopped),
			seed_ratio_limit = COALESCE(excluded.seed_ratio_limit, torrent_seed_criteria.seed_ratio_limit),
			seed_ratio_mode = COALESCE(excluded.seed_ratio_mode, torrent_seed_criteria.seed_ratio_mode),
			seed_idle_limit = COALESCE(excluded.seed_idle_limit, torrent_seed_criteria.seed_idle_limit),
			seed_idle_mode = COALESCE(excluded.seed_idle_mode, torrent_seed_criteria.seed_idle_mode)
	`, hash, criteria.SeedRatioLimit, criteria.SeedRatioMode, criteria.SeedIdleLimit, criteria.SeedIdleMode, boolToNullInt(criteria.Stopped))

	return err
}

func boolToNullInt(value *bool) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}

	if *value {
		return sql.NullInt64{Int64: 1, Valid: true}
	}

	return sql.NullInt64{Int64: 0, Valid: true}
}

// GetTorrentHash returns the original torrent infohash associated with a Put.io transfer.
func (r *DownloadRepository) GetTorrentHash(transferID string) (string, error) {
	var hash string

	err := r.db.QueryRow(`SELECT hash FROM torrent_seed_criteria WHERE transfer_id = ?`, transferID).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}

	return hash, err
}

// GetTorrentSeedCriteria retrieves per-torrent seed limits by Transmission hash.
func (r *DownloadRepository) GetTorrentSeedCriteria(transferID, hash string) (storage.TorrentSeedCriteria, error) {
	var criteria storage.TorrentSeedCriteria

	var ratio sql.NullFloat64

	var ratioMode, idleLimit, idleMode sql.NullInt64

	var stopped sql.NullInt64

	err := r.db.QueryRow(`
		SELECT seed_ratio_limit, seed_ratio_mode, seed_idle_limit, seed_idle_mode, stopped
		FROM torrent_seed_criteria WHERE transfer_id = ? OR hash = ?
		ORDER BY CASE WHEN transfer_id = ? THEN 0 ELSE 1 END LIMIT 1
	`, transferID, hash, transferID).Scan(&ratio, &ratioMode, &idleLimit, &idleMode, &stopped)
	if err != nil {
		if err == sql.ErrNoRows {
			return criteria, nil
		}

		return criteria, err
	}

	if ratio.Valid {
		criteria.SeedRatioLimit = &ratio.Float64
	}

	if ratioMode.Valid {
		criteria.SeedRatioMode = &ratioMode.Int64
	}

	if idleLimit.Valid {
		criteria.SeedIdleLimit = &idleLimit.Int64
	}

	if idleMode.Valid {
		criteria.SeedIdleMode = &idleMode.Int64
	}

	if stopped.Valid {
		value := stopped.Int64 != 0
		criteria.Stopped = &value
	}

	return criteria, nil
}

// ResolveTorrentID maps an infohash to its Put.io transfer ID.
func (r *DownloadRepository) ResolveTorrentID(hash string) (string, error) {
	var transferID string

	err := r.db.QueryRow(`SELECT transfer_id FROM torrent_seed_criteria WHERE hash = ?`, hash).Scan(&transferID)
	if err == sql.ErrNoRows {
		return "", nil
	}

	return transferID, err
}

// DeleteTorrentSeedCriteria removes persisted Transmission seed limits by torrent hash.
func (r *DownloadRepository) DeleteTorrentSeedCriteria(hash string) error {
	_, err := r.db.Exec(`DELETE FROM torrent_seed_criteria WHERE hash = ?`, hash)

	return err
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
