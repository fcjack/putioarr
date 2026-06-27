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
	FilePath     string
	DownloadedAt string
	Status       string
	LockedBy     string
}

type DownloadRepository interface {
	GetDownloads() ([]DownloadRecord, error)                   // get all downloads
	GetByTransferID(transferID string) (DownloadRecord, error) // get a single download record
	ClaimTransfer(transferID string) (bool, error)             // atomically claim a transfer
	UpdateTransferStatus(transferID, status string) error      // update status after download
	DeleteByTransferID(transferID string) error                // delete a single download record
	ResetDownloads() error                                     // wipe all download records
}
