package sqlite

import (
	"context"
	"database/sql"

	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/telemetry"
)

// InstrumentedDownloadRepository wraps DownloadRepository with telemetry.
type InstrumentedDownloadRepository struct {
	repo      *DownloadRepository
	telemetry *telemetry.Telemetry
}

// NewInstrumentedDownloadRepository creates a new instrumented download repository.
func NewInstrumentedDownloadRepository(dbConn *sql.DB, tel *telemetry.Telemetry) *InstrumentedDownloadRepository {
	return &InstrumentedDownloadRepository{
		repo:      NewDownloadRepository(dbConn),
		telemetry: tel,
	}
}

// GetDownloads retrieves all downloads with telemetry.
func (r *InstrumentedDownloadRepository) GetDownloads() ([]storage.DownloadRecord, error) {
	var result []storage.DownloadRecord

	var err error

	instrumentedErr := r.telemetry.InstrumentDBOperation(context.Background(), "get_downloads", func(ctx context.Context) error {
		result, err = r.repo.GetDownloads()

		return err
	})

	if instrumentedErr != nil {
		return nil, instrumentedErr
	}

	return result, nil
}

// GetByTransferID retrieves a single download record with telemetry.
func (r *InstrumentedDownloadRepository) GetByTransferID(transferID string) (storage.DownloadRecord, error) {
	var result storage.DownloadRecord

	var err error

	instrumentedErr := r.telemetry.InstrumentDBOperation(context.Background(), "get_by_transfer_id", func(ctx context.Context) error {
		result, err = r.repo.GetByTransferID(transferID)

		return err
	})

	if instrumentedErr != nil {
		return storage.DownloadRecord{}, instrumentedErr
	}

	return result, nil
}

// ClaimTransfer claims a transfer with telemetry.
func (r *InstrumentedDownloadRepository) ClaimTransfer(transferID, name string) (bool, error) {
	var result bool

	var err error

	instrumentedErr := r.telemetry.InstrumentDBOperation(context.Background(), "claim_transfer", func(ctx context.Context) error {
		result, err = r.repo.ClaimTransfer(transferID, name)

		return err
	})

	if instrumentedErr != nil {
		return false, instrumentedErr
	}

	return result, nil
}

// UpdateTransferStatus updates transfer status with telemetry.
func (r *InstrumentedDownloadRepository) UpdateTransferStatus(transferID, status string) error {
	return r.telemetry.InstrumentDBOperation(context.Background(), "update_transfer_status", func(ctx context.Context) error {
		return r.repo.UpdateTransferStatus(transferID, status)
	})
}

// DeleteByTransferID removes a single download record with telemetry.
func (r *InstrumentedDownloadRepository) DeleteByTransferID(transferID string) error {
	return r.telemetry.InstrumentDBOperation(context.Background(), "delete_by_transfer_id", func(ctx context.Context) error {
		return r.repo.DeleteByTransferID(transferID)
	})
}

// ResetDownloads wipes the downloads table with telemetry.
func (r *InstrumentedDownloadRepository) ResetDownloads() error {
	return r.telemetry.InstrumentDBOperation(context.Background(), "reset_downloads", func(ctx context.Context) error {
		return r.repo.ResetDownloads()
	})
}
