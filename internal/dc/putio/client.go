package putio

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
	"github.com/putdotio/go-putio"
	"golang.org/x/oauth2"
)

const maxTorrentSize = 10 * 1024 * 1024 // 10MB max torrent file size

var (
	// ErrTransferFilesNotFound indicates the transfer's files were deleted from Put.io
	// but the transfer itself may still exist.
	ErrTransferFilesNotFound = errors.New("transfer files not found on Put.io")

	// ErrTransferNotFound indicates the transfer itself no longer exists on Put.io.
	// Detected when a previously-claimed transfer arrives at DownloadTransfer with zero files.
	ErrTransferNotFound = errors.New("transfer not found on Put.io")
)

type Client struct {
	putioClient *putio.Client
}

func NewClient(token string, insecure ...bool) *Client {
	client := &Client{}

	// Initialize Put.io client
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	oauthClient := oauth2.NewClient(context.Background(), tokenSource)
	client.putioClient = putio.NewClient(oauthClient)

	return client
}

// GetTaggedTorrents retrieves torrents matching the given tag from Put.io.
func (c *Client) GetTaggedTorrents(ctx context.Context, tag string) ([]*transfer.Transfer, error) {
	logger := logctx.LoggerFromContext(ctx).With("tag", tag)

	transfers, err := c.putioClient.Transfers.List(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "failed to get transfers", "err", err)

		return nil, fmt.Errorf("failed to get transfers: %w", err)
	}

	torrents := make([]*transfer.Transfer, 0, len(transfers))

	for _, t := range transfers {
		// Use SaveParentID for tag matching (works for both in-progress and completed transfers)
		if t.SaveParentID == 0 {
			logger.DebugContext(ctx, "skipping transfer with no save parent", "transfer_id", t.ID, "status", t.Status)

			continue
		}

		parent, err := c.putioClient.Files.Get(ctx, t.SaveParentID)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get parent folder", "transfer_id", t.ID, "save_parent_id", t.SaveParentID, "err", err)

			continue
		}

		if parent.IsDir() && parent.Name != tag {
			logger.DebugContext(ctx, "skipping transfer, parent folder doesn't match tag",
				"transfer_id", t.ID, "parent_name", parent.Name, "expected_tag", tag)

			continue
		}

		// Convert Put.io transfer to our Torrent type
		torrent := &transfer.Transfer{
			ID:                 fmt.Sprintf("%d", t.ID),
			Name:               t.Name,
			Label:              tag,
			Progress:           float64(t.PercentDone),
			Files:              make([]*transfer.File, 0),
			Size:               int64(t.Size),
			Source:             t.Source,
			Status:             t.Status,
			EstimatedTime:      t.EstimatedTime,
			SavePath:           "/" + tag,
			PeersConnected:     int64(t.PeersConnected),
			PeersGettingFromUs: int64(t.PeersGettingFromUs),
			PeersSendingToUs:   int64(t.PeersSendingToUs),
			Downloaded:         int64(t.Downloaded),
			DownloadSpeed:      int64(t.DownloadSpeed),
		}

		// Only fetch file details and populate Files for completed transfers (FileID != 0)
		if t.FileID != 0 {
			file, err := c.putioClient.Files.Get(ctx, t.FileID)
			if err != nil {
				logger.WarnContext(ctx, "transfer files missing from Put.io during listing", "transfer_id", t.ID, "file_id", t.FileID, "err", err)

				continue
			}

			// Populate files for completed transfers. On error, continue anyway --
			// transfer stays visible in Activity tab, download pipeline skips via IsDownloadable().
			//
			// Root the file tree under the transfer name (not the Put.io file/folder name).
			// Sonarr/Radarr learn the expected download path from the transfer name we advertise
			// via the Transmission RPC (torrent-get). Put.io's torrent-internal folder/file name
			// frequently differs from that release name, so using it as the on-disk root caused
			// the *arr import to look for a path that never existed. See issue #5.
			files, err := c.getFilesRecursively(ctx, file.ID, t.Name)
			if err != nil {
				logger.ErrorContext(ctx, "failed to get files for completed transfer",
					"transfer_id", t.ID, "file_id", t.FileID, "err", err)
			} else {
				torrent.Files = append(torrent.Files, files...)
			}
		}

		torrents = append(torrents, torrent)
	}

	logger.DebugContext(ctx, "found torrents to download", "torrent_count", len(torrents))

	return torrents, nil
}

// GetTransferInfo retrieves the upload ratio for a transfer by its string ID.
// Returns (uploadRatio, found, error). If the transfer is not found, found is false and err is nil.
// If Downloaded is zero, ratio is returned as 0 to avoid division by zero.
func (c *Client) GetTransferInfo(ctx context.Context, transferID string) (float64, bool, error) {
	transfers, err := c.putioClient.Transfers.List(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("failed to list transfers: %w", err)
	}

	for _, t := range transfers {
		if fmt.Sprintf("%d", t.ID) == transferID {
			if t.Downloaded == 0 {
				return 0, true, nil
			}

			ratio := float64(t.Uploaded) / float64(t.Downloaded)

			return ratio, true, nil
		}
	}

	return 0, false, nil
}

// GrabFile implements DownloadClient.GrabFile for Put.io.
func (c *Client) GrabFile(ctx context.Context, file *transfer.File) (io.ReadCloser, error) {
	logger := logctx.LoggerFromContext(ctx)

	url, err := c.putioClient.Files.URL(ctx, file.ID, false)
	if err != nil {
		logger.ErrorContext(ctx, "failed to get file download url", "file_id", file.ID, "err", err)

		return nil, fmt.Errorf("file %d no longer available on Put.io: %w", file.ID, ErrTransferFilesNotFound)
	}

	resp, err := http.Get(url)
	if err != nil {
		logger.ErrorContext(ctx, "failed to get file", "file_id", file.ID, "err", err)

		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return resp.Body, nil
}

func (c *Client) Authenticate(ctx context.Context) error {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "authenticating with Put.io")

	user, err := c.putioClient.Account.Info(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "failed to get account info", "err", err)

		return fmt.Errorf("failed to get account info: %w", err)
	}

	logger.InfoContext(ctx, "authenticated with Put.io", "user", user.Username)

	return nil
}

// validateTorrentFilename validates that the filename has a .torrent extension.
func validateTorrentFilename(filename string) error {
	ext := filepath.Ext(filename)
	if !strings.EqualFold(ext, ".torrent") {
		return &transfer.InvalidContentError{
			Filename: filename,
			Reason:   "file extension must be .torrent (Put.io requires extension for transfer detection)",
		}
	}

	return nil
}

// Deprecated: Use AddTransferByURL for magnet/HTTP(S) links or AddTransferByBytes for .torrent file content.
// This method will be removed in a future version.
func (c *Client) AddTransfer(ctx context.Context, url string, downloadDir string) (*transfer.Transfer, error) {
	logger := logctx.LoggerFromContext(ctx).With("download_dir", downloadDir)

	var dirID int64

	if downloadDir != "" {
		var err error

		dirID, err = c.findDirectoryID(ctx, downloadDir)
		if err != nil {
			return nil, fmt.Errorf("failed to find directory: %w", err)
		}
	}

	logger.InfoContext(ctx, "adding transfer to Put.io", "transfer_url", url)

	t, err := c.putioClient.Transfers.Add(ctx, url, dirID, "")
	if err != nil {
		return nil, fmt.Errorf("failed to add transfer: %w", err)
	}

	logger.InfoContext(ctx, "transfer added to Put.io", "transfer_id", t.ID)

	return &transfer.Transfer{
		ID:                 fmt.Sprintf("%d", t.ID),
		Name:               t.Name,
		Downloaded:         t.Downloaded,
		Size:               int64(t.Size),
		EstimatedTime:      t.EstimatedTime,
		Status:             t.Status,
		Progress:           float64(t.PercentDone),
		Files:              make([]*transfer.File, 0),
		Source:             t.Source,
		PeersConnected:     int64(t.PeersConnected),
		PeersGettingFromUs: int64(t.PeersGettingFromUs),
	}, nil
}

// AddTransferByBytes adds a transfer by uploading .torrent file bytes to Put.io.
// The Put.io service automatically detects .torrent files and creates a transfer.
func (c *Client) AddTransferByBytes(ctx context.Context, torrentBytes []byte, filename string, downloadDir string) (*transfer.Transfer, error) {
	logger := logctx.LoggerFromContext(ctx).With("filename", filename, "download_dir", downloadDir)

	// Validate file size
	if len(torrentBytes) > maxTorrentSize {
		return nil, &transfer.InvalidContentError{
			Filename: filename,
			Reason:   fmt.Sprintf("file size %d bytes exceeds maximum %d bytes", len(torrentBytes), maxTorrentSize),
		}
	}

	// Validate file extension
	if err := validateTorrentFilename(filename); err != nil {
		return nil, err
	}

	// Resolve directory (same logic as magnet links)
	var dirID int64

	if downloadDir != "" {
		var err error

		dirID, err = c.findDirectoryID(ctx, downloadDir)
		if err != nil {
			return nil, &transfer.DirectoryError{
				DirectoryName: downloadDir,
				Reason:        "directory not found or inaccessible",
				Err:           err,
			}
		}
	}

	// Convert bytes to io.Reader
	reader := bytes.NewReader(torrentBytes)

	logger.InfoContext(ctx, "uploading torrent file to Put.io", "size_bytes", len(torrentBytes))

	// Upload to Put.io
	upload, err := c.putioClient.Files.Upload(ctx, reader, filename, dirID)
	if err != nil {
		return nil, &transfer.NetworkError{
			Operation:  "upload_torrent",
			APIMessage: err.Error(),
			Err:        err,
		}
	}

	// Put.io automatically creates transfer for .torrent files
	if upload.Transfer == nil {
		return nil, &transfer.InvalidContentError{
			Filename: filename,
			Reason:   "Put.io did not create transfer (file may not be valid torrent)",
		}
	}

	logger.InfoContext(ctx, "transfer created from torrent upload", "transfer_id", upload.Transfer.ID)

	// Convert to internal transfer type (same pattern as existing AddTransfer)
	return &transfer.Transfer{
		ID:                 fmt.Sprintf("%d", upload.Transfer.ID),
		Name:               upload.Transfer.Name,
		Downloaded:         upload.Transfer.Downloaded,
		Size:               int64(upload.Transfer.Size),
		EstimatedTime:      upload.Transfer.EstimatedTime,
		Status:             upload.Transfer.Status,
		Progress:           float64(upload.Transfer.PercentDone),
		Files:              make([]*transfer.File, 0),
		Source:             upload.Transfer.Source,
		PeersConnected:     int64(upload.Transfer.PeersConnected),
		PeersGettingFromUs: int64(upload.Transfer.PeersGettingFromUs),
		PeersSendingToUs:   int64(upload.Transfer.PeersSendingToUs),
	}, nil
}

// RemoveTransfers implements DownloadClient.RemoveTransfers for Put.io. The transferIDs are the hashes of the transfers.
func (c *Client) RemoveTransfers(ctx context.Context, transferIDs []string, deleteFiles bool) error {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "removing transfer from Put.io", "transfer_ids", transferIDs)

	transfers, err := c.putioClient.Transfers.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to get transfers: %w", err)
	}

	putioTransfers := c.filterMatchingTransferIds(transfers, transferIDs)

	if len(putioTransfers) == 0 {
		return fmt.Errorf("transfer not found: %v", transferIDs)
	}

	for _, transfer := range putioTransfers {
		if err := c.putioClient.Transfers.Cancel(ctx, transfer.ID); err != nil {
			return fmt.Errorf("failed to remove transfer: %w", err)
		}

		// If deleteLocalData is true and the file exists, delete it
		if deleteFiles && transfer.FileID != 0 {
			logger.InfoContext(ctx, "deleting local file data", "file_id", transfer.FileID)

			if err := c.putioClient.Files.Delete(ctx, transfer.FileID); err != nil {
				return fmt.Errorf("failed to delete local file data: %w", err)
			}

			logger.InfoContext(ctx, "local file data deleted", "file_id", transfer.FileID)
		}
	}

	return nil
}

func (c *Client) filterMatchingTransferIds(transfers []putio.Transfer, transferIDs []string) []putio.Transfer {
	matchingTransfers := make([]putio.Transfer, 0, len(transferIDs))

	for _, t := range transfers {
		hash := sha1.Sum([]byte(fmt.Sprintf("%d", t.ID)))
		hashString := hex.EncodeToString(hash[:])

		if slices.Contains(transferIDs, hashString) {
			matchingTransfers = append(matchingTransfers, t)
		}
	}

	return matchingTransfers
}

func (c *Client) findDirectoryID(ctx context.Context, downloadDir string) (int64, error) {
	search, err := c.putioClient.Files.Search(ctx, downloadDir, 1)
	if err != nil {
		return 0, fmt.Errorf("error searching for directory: %w", err)
	}

	if len(search.Files) == 0 {
		return 0, fmt.Errorf("directory not found: %s", downloadDir)
	}

	if !search.Files[0].IsDir() {
		return 0, fmt.Errorf("search result is not a directory: %s", downloadDir)
	}

	return search.Files[0].ID, nil
}

// getFilesRecursively walks the Put.io file tree rooted at parentID and returns the flat list
// of downloadable files. basePath is the local path prefix every returned file is rooted under;
// the top-level caller passes the transfer name so the on-disk layout matches the path that
// Sonarr/Radarr expect for import (see issue #5).
func (c *Client) getFilesRecursively(ctx context.Context, parentID int64, basePath string) ([]*transfer.File, error) {
	logger := logctx.LoggerFromContext(ctx).With("parent_id", parentID, "base_path", basePath)

	var result []*transfer.File

	file, err := c.putioClient.Files.Get(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	if !file.IsDir() {
		// Single-file transfer: place the file inside a folder named after basePath
		// (the transfer/release name). The folder name is used verbatim so it matches
		// exactly what is advertised to Sonarr/Radarr, guaranteeing the import path exists.
		result = append(result, &transfer.File{
			ID:   file.ID,
			Path: filepath.Join(basePath, file.Name),
			Size: file.Size,
		})

		return result, nil
	}

	files, _, err := c.putioClient.Files.List(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	for _, f := range files {
		switch strings.ToLower(f.FileType) {
		case "file", "text", "video", "audio", "archive":
			result = append(result, &transfer.File{
				ID:   f.ID,
				Path: filepath.Join(basePath, f.Name),
				Size: f.Size,
			})
		case "folder":
			nestedFiles, err := c.getFilesRecursively(ctx, f.ID, filepath.Join(basePath, f.Name))
			if err != nil {
				logger.ErrorContext(ctx, "failed to get nested files", "err", err)

				continue
			}

			result = append(result, nestedFiles...)
		}
	}

	return result, nil
}
