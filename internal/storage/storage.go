package storage

import "errors"

var (
	ErrDownloaded = errors.New("Download already completed")
	// ErrNotFound is returned when a download record does not exist for a transfer ID.
	ErrNotFound = errors.New("download record not found")
)

// DownloadRecord represents a record of a downloaded file.
type DownloadRecord struct {
	DownloadID   string
	Name         string
	FilePath     string
	DownloadedAt string
	Status       string
	LockedBy     string
}

// TorrentSeedCriteria stores per-torrent Transmission seed limits.
type TorrentSeedCriteria struct {
	Stopped        *bool
	SeedRatioLimit *float64
	SeedRatioMode  *int64
	SeedIdleLimit  *int64
	SeedIdleMode   *int64
}

type DownloadRepository interface {
	GetDownloads() ([]DownloadRecord, error)                   // get all downloads
	GetByTransferID(transferID string) (DownloadRecord, error) // get a single download record
	ClaimTransfer(transferID, name string) (bool, error)       // atomically claim a transfer
	UpdateTransferStatus(transferID, status string) error      // update status after download
	DeleteByTransferID(transferID string) error                // delete a single download record
	ResetDownloads() error                                     // wipe all download records
}

// TorrentSeedCriteriaRepository stores Transmission seeding limits by torrent hash.
type TorrentSeedCriteriaRepository interface {
	AssociateTorrentHash(hash, transferID string) error
	UpdateTorrentSeedCriteria(hash string, criteria TorrentSeedCriteria) error
	GetTorrentHash(transferID string) (string, error)
	GetTorrentSeedCriteria(transferID, hash string) (TorrentSeedCriteria, error)
	ResolveTorrentID(hash string) (string, error)
	DeleteTorrentSeedCriteria(hash string) error
}
