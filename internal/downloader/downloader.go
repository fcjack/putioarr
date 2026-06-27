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

type Downloader struct {
	downloadDir string
	dc          transfer.DownloadClient
	tc          transfer.TransferClient
	arrServices []*arr.Client
	maxParallel int

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
) *Downloader {
	return &Downloader{
		downloadDir:                downloadDir,
		dc:                         dc,
		maxParallel:                maxParallel,
		tc:                         tc,
		arrServices:                arrServices,
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
				imported, err := d.checkForImported(ctx, t)
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
			if strings.Contains(err.Error(), "transfer not found") {
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

func (d *Downloader) checkForImported(ctx context.Context, transfer *transfer.Transfer) (bool, error) {
	logger := logctx.LoggerFromContext(ctx)
	logger.DebugContext(ctx, "checking if transfer has been imported", "transfer_id", transfer.ID, "transfer_name", transfer.Name)

	for _, file := range transfer.Files {
		for _, arrService := range d.arrServices {
			filePath := filepath.Join(d.downloadDir, file.Path)

			imported, err := arrService.CheckImported(ctx, filePath)
			if err != nil {
				return false, fmt.Errorf("failed to check if transfer has been imported: %w", err)
			}

			if imported {
				logger.InfoContext(ctx, "transfer has been imported", "transfer_id", transfer.ID, "transfer_name", transfer.Name)

				if err := os.RemoveAll(filePath); err != nil {
					return false, fmt.Errorf("failed to remove file: %w", err)
				}

				logger.InfoContext(ctx, "transfer removed", "transfer_id", transfer.ID, "transfer_name", transfer.Name)

				return true, nil
			}
		}
	}

	return false, nil
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
