package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cenkalti/backoff/v5"
	// Import the SQLite driver.
	_ "github.com/mattn/go-sqlite3"
)

// InitDB initializes the SQLite database and creates the downloads table if it doesn't exist.
func InitDB(ctx context.Context, dbPath string, maxOpenConns, maxIdleConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Configure connection pool limits
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)

	// Validate database connectivity with exponential backoff retry
	retryCount := 0
	_, err = backoff.Retry(ctx, func() (struct{}, error) {
		if retryCount > 0 {
			slog.DebugContext(ctx, "retrying database ping", "attempt", retryCount+1)
		}

		retryCount++

		return struct{}{}, db.PingContext(ctx)
	}, backoff.WithMaxTries(3))

	if err != nil {
		db.Close()

		return nil, err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS downloads (
		transfer_id TEXT UNIQUE,
		downloaded_at DATETIME,
		status TEXT DEFAULT 'pending',
		locked_by TEXT,
		name TEXT
	)`)

	if err != nil {
		db.Close()

		return nil, err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS torrent_seed_criteria (
		hash TEXT PRIMARY KEY,
		transfer_id TEXT,
		seed_ratio_limit REAL,
		seed_ratio_mode INTEGER,
		seed_idle_limit INTEGER,
		seed_idle_mode INTEGER,
		stopped INTEGER
	)`)
	if err != nil {
		db.Close()

		return nil, fmt.Errorf("failed to create torrent seed criteria table: %w", err)
	}

	if err := ensureTorrentSeedCriteriaTransferIDColumn(db); err != nil {
		db.Close()

		return nil, err
	}

	if err := ensureTorrentSeedCriteriaStoppedColumn(db); err != nil {
		db.Close()

		return nil, err
	}

	// Migrate pre-existing databases that lack the name column. SQLite has no
	// "ADD COLUMN IF NOT EXISTS", so a duplicate-column error is expected and
	// safely ignored when the column is already present.
	if err := ensureNameColumn(db); err != nil {
		db.Close()

		return nil, err
	}

	return db, nil
}

func ensureTorrentSeedCriteriaStoppedColumn(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE torrent_seed_criteria ADD COLUMN stopped INTEGER`); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}

		return fmt.Errorf("failed to add torrent seed criteria stopped column: %w", err)
	}

	return nil
}

func ensureTorrentSeedCriteriaTransferIDColumn(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE torrent_seed_criteria ADD COLUMN transfer_id TEXT`); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}

		return fmt.Errorf("failed to add torrent seed criteria transfer ID column: %w", err)
	}

	return nil
}

// ensureNameColumn adds the downloads.name column to legacy databases. It is a
// no-op when the column already exists.
func ensureNameColumn(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE downloads ADD COLUMN name TEXT`); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}

		return fmt.Errorf("failed to add name column: %w", err)
	}

	return nil
}
