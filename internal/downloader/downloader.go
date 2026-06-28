package downloader

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/dustin/go-humanize"
	"github.com/italolelis/seedbox_downloader/internal/dc/putio"
	"github.com/italolelis/seedbox_downloader/internal/downloader/progress"
	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/svc/arr"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
	"golang.org/x/sync/errgroup"
)

// MissingTransferEvent carries a missing transfer and its classification.
type MissingTransferEvent struct {
	Transfer    *transfer.Transfer
	MissingType string // "files_missing" or "transfer_removed"
}

const (
	dirPerm = 0755
)

// CleanupOptions controls local post-import cleanup behavior under DOWNLOAD_DIR.
type CleanupOptions struct {
	// AfterImport enables removing local download artifacts once an *arr app
	// confirms the release was imported. When false, imports are still detected
	// (so Put.io cleanup proceeds) but local files are left in place.
	AfterImport bool
	// RemoveEmptyDirs prunes empty parent directories up to DOWNLOAD_DIR after the
	// release root is removed.
	RemoveEmptyDirs bool
	// SweepInterval is how often the background sweep runs to prune leftover empty
	// directories under DOWNLOAD_DIR. Zero or negative disables the sweep.
	SweepInterval time.Duration
	// SweepMinAge is the minimum age (by modification time) an empty directory must
	// reach before the sweep removes it, protecting in-progress downloads.
	SweepMinAge time.Duration
}

type Downloader struct {
	downloadDir string
	dc          transfer.DownloadClient
	tc          transfer.TransferClient
	arrServices []*arr.Client
	maxParallel int
	cleanup     CleanupOptions

	// activeDownloads maps an in-flight transfer ID to its cancel func so the UI can cancel it.
	activeDownloads sync.Map

	OnFileDownloadError        chan *transfer.File
	OnTransferDownloadError    chan *transfer.Transfer
	OnTransferDownloadFinished chan *transfer.Transfer
	OnTransferImported         chan *transfer.Transfer
	OnTransferMissing          chan MissingTransferEvent
}

func NewDownloader(
	downloadDir string,
	maxParallel int,
	dc transfer.DownloadClient,
	tc transfer.TransferClient,
	arrServices []*arr.Client,
	cleanup CleanupOptions,
) *Downloader {
	return &Downloader{
		downloadDir:                downloadDir,
		dc:                         dc,
		maxParallel:                maxParallel,
		tc:                         tc,
		arrServices:                arrServices,
		cleanup:                    cleanup,
		OnFileDownloadError:        make(chan *transfer.File),
		OnTransferDownloadError:    make(chan *transfer.Transfer),
		OnTransferDownloadFinished: make(chan *transfer.Transfer),
		OnTransferImported:         make(chan *transfer.Transfer),
		OnTransferMissing:          make(chan MissingTransferEvent),
	}
}

// CancelDownload cancels an in-flight local download for the given transfer ID.
// It returns true if a matching active download was found and signalled. The byte
// copy aborts at the next read chunk; the transfer is then reported as a download error.
func (d *Downloader) CancelDownload(transferID string) bool {
	value, ok := d.activeDownloads.Load(transferID)
	if !ok {
		return false
	}

	cancel, ok := value.(context.CancelFunc)
	if !ok {
		return false
	}

	cancel()

	return true
}

func (d *Downloader) Close() {
	close(d.OnFileDownloadError)
	close(d.OnTransferDownloadError)
	close(d.OnTransferDownloadFinished)
	close(d.OnTransferImported)
	close(d.OnTransferMissing)
}

// WatchDownloads watches for transfers and downloads them.
func (d *Downloader) WatchDownloads(ctx context.Context, incomingTransfers <-chan *transfer.Transfer) {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "watching downloads")

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "shutting down downloader")

				return
			case transfer := <-incomingTransfers:
				logger.DebugContext(ctx, "downloading transfer", "transfer_id", transfer.ID, "transfer_name", transfer.Name)

				dlCtx, cancel := context.WithCancel(ctx)
				d.activeDownloads.Store(transfer.ID, cancel)

				downloadedFiles, err := d.DownloadTransfer(dlCtx, transfer)

				d.activeDownloads.Delete(transfer.ID)
				cancel()

				if err != nil {
					if errors.Is(err, context.Canceled) {
						logger.WarnContext(ctx, "transfer download cancelled", "transfer_id", transfer.ID, "transfer_name", transfer.Name)
						d.OnTransferDownloadError <- transfer

						continue
					}

					if errors.Is(err, putio.ErrTransferNotFound) {
						logger.WarnContext(ctx, "transfer removed from Put.io", "transfer_id", transfer.ID, "transfer_name", transfer.Name)
						d.OnTransferMissing <- MissingTransferEvent{Transfer: transfer, MissingType: "transfer_removed"}

						continue
					}

					if errors.Is(err, putio.ErrTransferFilesNotFound) {
						// Warn log already emitted inside DownloadTransfer for the specific file
						d.OnTransferMissing <- MissingTransferEvent{Transfer: transfer, MissingType: "files_missing"}

						continue
					}

					logger.ErrorContext(ctx, "failed to download transfer", "download_id", transfer.ID, "err", err)

					d.OnTransferDownloadError <- transfer

					continue
				}

				if downloadedFiles > 0 {
					logger.InfoContext(ctx, "downloads completed", "download_id", transfer.ID, "transfer_name", transfer.Name)

					d.OnTransferDownloadFinished <- transfer
				}
			}
		}
	}()
}

// DownloadTransfer downloads a transfer and returns the number of files downloaded.
func (d *Downloader) DownloadTransfer(ctx context.Context, transfer *transfer.Transfer) (int, error) {
	var downloadedFiles int32

	wg, ctx := errgroup.WithContext(ctx)

	if len(transfer.Files) == 0 {
		return 0, fmt.Errorf("transfer %s has no files (transfer removed from Put.io): %w", transfer.Name, putio.ErrTransferNotFound)
	}

	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "starting download",
		"transfer_id", transfer.ID,
		"transfer_name", transfer.Name,
		"file_count", len(transfer.Files))

	sem := make(chan struct{}, d.maxParallel)

	for i := range transfer.Files {
		file := transfer.Files[i]
		sem <- struct{}{}

		wg.Go(func() error {
			defer func() { <-sem }() // release the slot

			targetPath := filepath.Join(d.downloadDir, file.Path)
			if err := d.DownloadFile(ctx, transfer.ID, file, targetPath); err != nil {
				if err == storage.ErrDownloaded {
					logger.DebugContext(ctx, "file already downloaded", "download_id", transfer.ID, "file_path", file.Path)

					return err
				}

				if errors.Is(err, putio.ErrTransferFilesNotFound) {
					logger.WarnContext(ctx, "transfer files missing from Put.io", "transfer_id", transfer.ID, "transfer_name", transfer.Name, "file_path", file.Path)
					os.RemoveAll(filepath.Join(d.downloadDir, transfer.Name))

					return fmt.Errorf("file %s missing from Put.io: %w", file.Path, putio.ErrTransferFilesNotFound)
				}

				logger.ErrorContext(ctx, "failed to download file", "download_id", transfer.ID, "file_path", file.Path, "err", err)

				return err
			}

			atomic.AddInt32(&downloadedFiles, 1)

			return nil
		})
	}

	if err := wg.Wait(); err != nil {
		return 0, fmt.Errorf("failed to download files: %w", err)
	}

	return int(downloadedFiles), nil
}

func (d *Downloader) DownloadFile(ctx context.Context, transferID string, file *transfer.File, targetPath string) error {
	logger := logctx.LoggerFromContext(ctx).With("transfer_id", transferID)

	fileReader, err := d.dc.GrabFile(ctx, file)
	if err != nil {
		return fmt.Errorf("failed to grab file: %w", err)
	}

	defer fileReader.Close()

	if err := d.ensureTargetDir(ctx, targetPath, logger); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}

	defer out.Close()

	if err := d.writeFile(ctx, out, fileReader, file.Path, targetPath, file.Size); err != nil {
		d.OnFileDownloadError <- file

		return fmt.Errorf("failed to download file: %w", err)
	}

	logger.DebugContext(ctx, "file downloaded", "target", targetPath)

	return nil
}

func (d *Downloader) WatchForImported(ctx context.Context, t *transfer.Transfer, pollingInterval time.Duration) {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "watching for imported transfers", "transfer_id", t.ID, "polling_interval", pollingInterval)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "watch imported panic",
					"operation", "watch_imported",
					"transfer_id", t.ID,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()

		ticker := time.NewTicker(pollingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "watch imported shutdown",
					"operation", "watch_imported",
					"transfer_id", t.ID,
					"reason", "context_cancelled")

				return
			case <-ticker.C:
				imported, err := d.cleanupAfterImport(ctx, t)
				if err != nil {
					logger.ErrorContext(ctx, "failed to check for imported transfer", "transfer_id", t.ID, "err", err)

					continue
				}

				if imported {
					logger.InfoContext(ctx, "transfer imported, stopping watch",
						"operation", "watch_imported",
						"transfer_id", t.ID,
						"reason", "transfer_imported")
					d.OnTransferImported <- t

					return
				}
			}
		}
	}()
}

// CleanupTransfer removes the Put.io transfer and its file data with exponential backoff retry.
// If the transfer is not found on Put.io (already deleted), this is treated as success.
// Cleanup failures after retries are logged but do not crash or stall the pipeline.
func (d *Downloader) CleanupTransfer(ctx context.Context, t *transfer.Transfer) {
	logger := logctx.LoggerFromContext(ctx)

	hash := sha1.Sum([]byte(t.ID))
	hashStr := hex.EncodeToString(hash[:])

	_, err := backoff.Retry[struct{}](ctx, func() (struct{}, error) {
		if err := d.tc.RemoveTransfers(ctx, []string{hashStr}, true); err != nil {
			if errors.Is(err, putio.ErrTransferNotFound) {
				logger.InfoContext(ctx, "Put.io transfer already removed, treating as success",
					"transfer_id", t.ID, "transfer_name", t.Name)

				return struct{}{}, nil
			}

			return struct{}{}, err
		}

		return struct{}{}, nil
	}, backoff.WithMaxTries(3))

	if err != nil {
		logger.ErrorContext(ctx, "failed to clean up Put.io transfer after retries, continuing",
			"transfer_id", t.ID, "err", err)

		return
	}

	logger.InfoContext(ctx, "Put.io transfer and files cleaned up",
		"transfer_id", t.ID, "transfer_name", t.Name)
}

// WatchForSeeding watches until the Put.io transfer reaches the target seed ratio, then cleans it up.
func (d *Downloader) WatchForSeeding(ctx context.Context, t *transfer.Transfer, pollingInterval time.Duration, seedRatio float64) {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "watching for seeding transfers",
		"transfer_id", t.ID, "polling_interval", pollingInterval, "seed_ratio", seedRatio)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "watch seeding panic",
					"operation", "watch_seeding",
					"transfer_id", t.ID,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()

		ticker := time.NewTicker(pollingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "watch seeding shutdown",
					"operation", "watch_seeding",
					"transfer_id", t.ID,
					"reason", "context_cancelled")

				return
			case <-ticker.C:
				infoer, ok := d.dc.(transfer.TransferInfoer)
				if !ok {
					logger.ErrorContext(ctx, "download client does not support transfer info, cannot watch seeding",
						"operation", "watch_seeding",
						"transfer_id", t.ID)

					return
				}

				uploadRatio, found, err := infoer.GetTransferInfo(ctx, t.ID)
				if err != nil {
					logger.ErrorContext(ctx, "failed to get transfer info, retrying next tick",
						"operation", "watch_seeding",
						"transfer_id", t.ID,
						"err", err)

					continue
				}

				if !found {
					logger.InfoContext(ctx, "transfer no longer exists on Put.io, cleanup already done",
						"operation", "watch_seeding",
						"transfer_id", t.ID)

					return
				}

				if uploadRatio >= seedRatio {
					logger.InfoContext(ctx, "seed ratio reached, cleaning up transfer",
						"operation", "watch_seeding",
						"transfer_id", t.ID,
						"upload_ratio", uploadRatio,
						"target_ratio", seedRatio)

					d.CleanupTransfer(ctx, t)

					return
				}

				logger.DebugContext(ctx, "seed ratio not yet reached",
					"operation", "watch_seeding",
					"transfer_id", t.ID,
					"upload_ratio", uploadRatio,
					"target_ratio", seedRatio)
			}
		}
	}()
}

// cleanupAfterImport asks each configured *arr app whether the transfer's release has
// been imported. On the first confirmation it removes the full release root (media and
// sidecars) under DOWNLOAD_DIR, prunes now-empty parent directories, and reports the
// transfer as imported. Nothing is deleted until an import is confirmed.
func (d *Downloader) cleanupAfterImport(ctx context.Context, t *transfer.Transfer) (bool, error) {
	logger := logctx.LoggerFromContext(ctx)

	releaseRoot := d.releaseRoot(t)

	logger.DebugContext(ctx, "checking if transfer has been imported",
		"transfer_id", t.ID, "transfer_name", t.Name, "release_root", releaseRoot)

	for _, arrService := range d.arrServices {
		if !arrService.IsConfigured() {
			continue
		}

		imported, err := arrService.CheckImported(ctx, releaseRoot)
		if err != nil {
			return false, fmt.Errorf("failed to check if transfer has been imported via %s: %w", arrService.Name(), err)
		}

		if !imported {
			continue
		}

		logger.InfoContext(ctx, "transfer has been imported",
			"transfer_id", t.ID, "transfer_name", t.Name, "imported_by", arrService.Name(), "release_root", releaseRoot)

		if err := d.removeReleaseRoot(ctx, t, releaseRoot); err != nil {
			return false, err
		}

		return true, nil
	}

	return false, nil
}

// releaseRoot returns the absolute path of a transfer's release under DOWNLOAD_DIR.
// Single-file transfers map to a flat file at DOWNLOAD_DIR/<name>; multi-file transfers
// map to the directory DOWNLOAD_DIR/<name>/ (issue #2 path semantics).
func (d *Downloader) releaseRoot(t *transfer.Transfer) string {
	return filepath.Join(d.downloadDir, t.Name)
}

// removeReleaseRoot deletes the release root and prunes empty parents, gated on the
// configured cleanup options. Every deletion is structured-logged.
func (d *Downloader) removeReleaseRoot(ctx context.Context, t *transfer.Transfer, releaseRoot string) error {
	logger := logctx.LoggerFromContext(ctx)

	if !d.cleanup.AfterImport {
		logger.InfoContext(ctx, "local cleanup disabled, leaving release in place",
			"transfer_id", t.ID, "transfer_name", t.Name, "release_root", releaseRoot)

		return nil
	}

	if err := os.RemoveAll(releaseRoot); err != nil {
		return fmt.Errorf("failed to remove release root %s: %w", releaseRoot, err)
	}

	logger.InfoContext(ctx, "release removed",
		"transfer_id", t.ID, "transfer_name", t.Name, "removed_path", releaseRoot)

	if d.cleanup.RemoveEmptyDirs {
		d.removeEmptyParents(ctx, filepath.Dir(releaseRoot))
	}

	return nil
}

// removeEmptyParents walks upward from dir removing empty directories until it reaches
// (and stops at) DOWNLOAD_DIR, a non-empty directory, or an error. DOWNLOAD_DIR itself
// is never removed, and the walk never escapes above it.
func (d *Downloader) removeEmptyParents(ctx context.Context, dir string) {
	logger := logctx.LoggerFromContext(ctx)

	stopAt := filepath.Clean(d.downloadDir)

	for {
		current := filepath.Clean(dir)

		// Stop at DOWNLOAD_DIR itself or anything not strictly nested under it.
		if current == stopAt || !strings.HasPrefix(current, stopAt+string(os.PathSeparator)) {
			return
		}

		entries, err := os.ReadDir(current)
		if err != nil {
			logger.WarnContext(ctx, "failed to read directory while pruning empty parents", "dir", current, "err", err)

			return
		}

		if len(entries) > 0 {
			return
		}

		if err := os.Remove(current); err != nil {
			logger.WarnContext(ctx, "failed to remove empty directory", "dir", current, "err", err)

			return
		}

		logger.InfoContext(ctx, "removed empty parent directory", "removed_path", current)

		dir = filepath.Dir(current)
	}
}

func (d *Downloader) ensureTargetDir(ctx context.Context, targetPath string, logger *slog.Logger) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		logger.ErrorContext(ctx, "failed to create target directory", "dir", dir, "err", err)

		return fmt.Errorf("failed to create target directory: %w", err)
	}

	return nil
}

func (d *Downloader) writeFile(ctx context.Context, out *os.File, reader io.Reader, url, targetPath string, totalBytes int64) error {
	logger := logctx.LoggerFromContext(ctx)

	logger.DebugContext(ctx, "downloading file", "file_path", targetPath, "file_size", humanize.Bytes(uint64(totalBytes)))

	progressInterval := int64(100 * 1024 * 1024) // 100MB
	progressCb := func(written int64, total int64) {
		if total > 0 {
			logger.DebugContext(ctx, "download progress",
				"url", url,
				"downloaded", humanize.Bytes(uint64(written)),
				"total", humanize.Bytes(uint64(total)),
				"percent", humanize.FtoaWithDigits(float64(written)*100/float64(total), 2))
		} else {
			logger.DebugContext(ctx, "download progress", "url", url, "downloaded", humanize.Bytes(uint64(written)))
		}
	}
	pr := progress.NewReader(&contextReader{ctx: ctx, reader: reader}, totalBytes, progressInterval, progressCb)

	if _, err := io.Copy(out, pr); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// contextReader aborts a read as soon as the context is cancelled so an in-flight
// download stops promptly when CancelDownload is called.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}

	return c.reader.Read(p)
}
