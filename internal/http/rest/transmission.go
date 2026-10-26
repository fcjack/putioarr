package rest

import (
	"context"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/telemetry"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
	"github.com/zeebo/bencode"
)

const sessionID = "useless-session-id"

const maxTorrentSize = 10 * 1024 * 1024 // 10MB - matches Phase 4 limit

const (
	defaultSeedRatioLimit  = 1.0
	defaultSeedTimeMinutes = 4320
)

// DownloadClient defines the interface for torrent client operations.
// This interface enables mocking in tests while the production code uses *putio.Client.
type DownloadClient interface {
	AddTransfer(ctx context.Context, magnetLink, parentName string) (*transfer.Transfer, error)
	AddTransferByBytes(ctx context.Context, content []byte, filename, parentName string) (*transfer.Transfer, error)
	GetTaggedTorrents(ctx context.Context, label string) ([]*transfer.Transfer, error)
	RemoveTransfers(ctx context.Context, ids []string, deleteData bool) error
}

type TransmissionTorrentStatus int

const (
	StatusStopped TransmissionTorrentStatus = iota
	StatusCheckWait
	StatusCheck
	StatusDownloadWait
	StatusDownload
	StatusSeedWait
	StatusSeed
)

type TransmissionTorrent struct {
	ID                 int64                     `json:"id"`
	HashString         string                    `json:"hashString,omitempty"`
	Name               string                    `json:"name"`
	DownloadDir        string                    `json:"downloadDir"`
	TotalSize          int64                     `json:"totalSize"`
	LeftUntilDone      int64                     `json:"leftUntilDone"`
	IsFinished         bool                      `json:"isFinished"`
	ETA                int64                     `json:"eta"`
	Status             TransmissionTorrentStatus `json:"status"`
	SecondsDownloading int64                     `json:"secondsDownloading"`
	ErrorString        *string                   `json:"errorString,omitempty"`
	DownloadedEver     int64                     `json:"downloadedEver"`
	UploadedEver       int64                     `json:"uploadedEver"`
	UploadRatio        float64                   `json:"uploadRatio"`
	SecondsSeeding     int64                     `json:"secondsSeeding"`
	Labels             []string                  `json:"labels"`
	PeersConnected     int64                     `json:"peersConnected"`
	PeersSendingToUs   int64                     `json:"peersSendingToUs"`
	PeersGettingFromUs int64                     `json:"peersGettingFromUs"`
	RateDownload       int64                     `json:"rateDownload"`
	SeedRatioLimit     float32                   `json:"seedRatioLimit"`
	SeedRatioMode      uint32                    `json:"seedRatioMode"`
	SeedIdleLimit      uint64                    `json:"seedIdleLimit"`
	SeedIdleMode       uint32                    `json:"seedIdleMode"`
	FileCount          uint32                    `json:"fileCount"`
}

type TransmissionResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

type TransmissionRequest struct {
	Method    string `json:"method"`
	Arguments struct {
		Fields          []string `json:"fields"`
		IDs             []string `json:"ids"`
		Format          string   `json:"format"`
		FileName        string   `json:"filename"`
		Paused          bool     `json:"paused"`
		DownloadDir     string   `json:"download-dir"`
		Labels          []string `json:"labels"`
		MetaInfo        string   `json:"metainfo"`
		SeedRatioLimit  *float64 `json:"seedRatioLimit"`
		SeedRatioMode   *int64   `json:"seedRatioMode"`
		SeedIdleLimit   *int64   `json:"seedIdleLimit"`
		SeedIdleMode    *int64   `json:"seedIdleMode"`
		DeleteLocalData bool     `json:"delete-local-data"`
	} `json:"arguments"`
}

type TransmissionConfig struct {
	RPCVersion              string  `json:"rpc-version"`
	Version                 string  `json:"version"`
	DownloadDir             string  `json:"download-dir"`
	SeedRatioLimit          float32 `json:"seedRatioLimit"`
	SeedRatioLimited        bool    `json:"seedRatioLimited"`
	IdleSeedingLimit        uint64  `json:"idle-seeding-limit"`
	IdleSeedingLimitEnabled bool    `json:"idle-seeding-limit-enabled"`
}

func NewTransmissionConfig(downloadDir string) *TransmissionConfig {
	return &TransmissionConfig{
		RPCVersion:              "18",
		Version:                 "14.0.0",
		DownloadDir:             downloadDir,
		SeedRatioLimit:          defaultSeedRatioLimit,
		SeedRatioLimited:        true,
		IdleSeedingLimit:        0,
		IdleSeedingLimitEnabled: false,
	}
}

// LocalDownloadTracker exposes persisted local download state for Transmission RPC.
type LocalDownloadTracker interface {
	GetDownloads() ([]storage.DownloadRecord, error)
}

type TorrentSeedCriteriaTracker interface {
	storage.TorrentSeedCriteriaRepository
}

type TransmissionHandler struct {
	username         string
	password         string
	dc               DownloadClient
	label            string
	downloadDir      string
	localDownloadDir string
	localDownloads   LocalDownloadTracker
	seedCriteria     TorrentSeedCriteriaTracker
	telemetry        *telemetry.Telemetry
}

// NewTransmissionHandler creates a new content handler.
func NewTransmissionHandler(
	username, password string,
	dc DownloadClient,
	label string,
	downloadDir string,
	localDownloadDir string,
	localDownloads LocalDownloadTracker,
	t *telemetry.Telemetry,
) *TransmissionHandler {
	handler := &TransmissionHandler{
		username:         username,
		password:         password,
		dc:               dc,
		label:            label,
		downloadDir:      downloadDir,
		localDownloadDir: localDownloadDir,
		localDownloads:   localDownloads,
		telemetry:        t,
	}
	if tracker, ok := localDownloads.(TorrentSeedCriteriaTracker); ok {
		handler.seedCriteria = tracker
	}

	return handler
}

func (h *TransmissionHandler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.basicAuthMiddleware)

	r.Post("/transmission/rpc", h.HandleRPC)
	r.Get("/transmission/rpc", h.HandleRPCGet)

	return r
}

// HandleRPC responsible to receive the callback from a webhook.
//
//nolint:funlen // RPC method dispatch and protocol response handling stay together.
func (h *TransmissionHandler) HandleRPC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logctx.LoggerFromContext(ctx)
	logger.DebugContext(ctx, "received post rpc request")

	var req TransmissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.ErrorContext(ctx, "failed to decode request", "err", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	var response *TransmissionResponse

	var err error

	switch req.Method {
	case "session-get":
		tConfig := NewTransmissionConfig(h.downloadDir)

		w.Header().Set("Content-Type", "application/json")

		jsonConfig, err := json.Marshal(tConfig)
		if err != nil {
			logger.ErrorContext(ctx, "failed to marshal config", "err", err)
			http.Error(w, "failed to marshal config", http.StatusInternalServerError)

			return
		}

		response = &TransmissionResponse{
			Result:    "success",
			Arguments: jsonConfig,
		}
	case "torrent-get":
		response, err = h.handleTorrentGet(ctx)
	case "torrent-set":
		response, err = h.handleTorrentSet(ctx, &req)
	case "torrent-start":
		response, err = h.handleTorrentStart(ctx, &req)
	case "torrent-stop":
		response, err = h.handleTorrentStop(ctx, &req)
	case "queue-move-top":
		// Nothing to do here
		response = &TransmissionResponse{
			Result: "success",
		}
	case "torrent-remove":
		response, err = h.handleTorrentRemove(ctx, &req)
	case "torrent-add":
		response, err = h.handleTorrentAdd(ctx, &req)
	default:
		logger.ErrorContext(ctx, "unknown method", "method", req.Method)
		http.Error(w, fmt.Sprintf("unknown method %s", req.Method), http.StatusBadRequest)

		return
	}

	if err != nil {
		logger.ErrorContext(ctx, "failed to handle request", "method", req.Method, "err", err)

		// Transmission RPC returns HTTP 200 with error in result field
		// This allows clients to display specific error messages
		errorResponse := &TransmissionResponse{
			Result: formatTransmissionError(err),
		}

		w.Header().Set("Content-Type", "application/json")

		if encodeErr := json.NewEncoder(w).Encode(errorResponse); encodeErr != nil {
			// Only use HTTP error for server-side failures (encoding)
			logger.ErrorContext(ctx, "failed to encode error response", "err", encodeErr)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.ErrorContext(ctx, "failed to encode response", "err", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)

		return
	}
}

// HandleRPCGet handles GET requests to the RPC endpoint.
func (h *TransmissionHandler) HandleRPCGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Transmission-Session-Id", sessionID)
	w.WriteHeader(http.StatusConflict)
	w.Write([]byte("{}"))
}

func (h *TransmissionHandler) basicAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			http.Error(w, "invalid authorization format", http.StatusUnauthorized)

			return
		}

		if username != h.username || password != h.password {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)

			return
		}

		next.ServeHTTP(w, r)
	})
}

// validateBencodeStructure validates that data is proper bencode torrent structure.
func validateBencodeStructure(data []byte) error {
	var torrentData interface{}

	// Decode bencode structure
	if err := bencode.DecodeBytes(data, &torrentData); err != nil {
		return &transfer.InvalidContentError{
			Filename: "metainfo",
			Reason:   fmt.Sprintf("invalid bencode structure: %v", err),
			Err:      err,
		}
	}

	// Verify root is a dictionary
	dict, ok := torrentData.(map[string]interface{})
	if !ok {
		return &transfer.InvalidContentError{
			Filename: "metainfo",
			Reason:   "bencode root must be a dictionary",
		}
	}

	// Check for required 'info' field
	if _, hasInfo := dict["info"]; !hasInfo {
		return &transfer.InvalidContentError{
			Filename: "metainfo",
			Reason:   "bencode missing required 'info' dictionary",
		}
	}

	return nil
}

// generateTorrentFilename generates a unique .torrent filename from torrent content.
func generateTorrentFilename(torrentBytes []byte) string {
	hash := sha1.Sum(torrentBytes)
	hashStr := hex.EncodeToString(hash[:])

	return fmt.Sprintf("%s.torrent", hashStr[:16])
}

// handleTorrentAddByMetaInfo processes .torrent file content from MetaInfo field.
func (h *TransmissionHandler) handleTorrentAddByMetaInfo(ctx context.Context, req *TransmissionRequest) (*transfer.Transfer, error) {
	logger := logctx.LoggerFromContext(ctx)

	// Decode base64 content (requirement API-02)
	torrentBytes, err := base64.StdEncoding.DecodeString(req.Arguments.MetaInfo)
	if err != nil {
		logger.ErrorContext(ctx, "failed to decode base64 metainfo",
			"err", err,
			"error_type", "invalid_base64",
			"metainfo_length", len(req.Arguments.MetaInfo),
		)

		return nil, &transfer.InvalidContentError{
			Filename: "metainfo",
			Reason:   fmt.Sprintf("invalid base64 encoding: %v", err),
			Err:      err,
		}
	}

	logger.DebugContext(ctx, "decoded metainfo", "size_bytes", len(torrentBytes))

	// Check size BEFORE bencode validation (prevent memory exhaustion)
	if len(torrentBytes) > maxTorrentSize {
		return nil, &transfer.InvalidContentError{
			Filename: "metainfo",
			Reason:   fmt.Sprintf("size %d bytes exceeds maximum %d bytes", len(torrentBytes), maxTorrentSize),
		}
	}

	// Validate bencode structure (requirement API-03)
	if err := validateBencodeStructure(torrentBytes); err != nil {
		logger.ErrorContext(ctx, "bencode validation failed",
			"err", err,
			"error_type", "invalid_bencode",
			"size_bytes", len(torrentBytes),
		)

		return nil, err
	}

	// Generate filename for Put.io (requires .torrent extension)
	filename := generateTorrentFilename(torrentBytes)
	logger.DebugContext(ctx, "generated filename", "filename", filename)

	// Upload to Put.io using Phase 4 client method
	torrent, err := h.dc.AddTransferByBytes(ctx, torrentBytes, filename, h.label)
	if err != nil {
		logger.ErrorContext(ctx, "failed to add transfer by bytes",
			"err", err,
			"error_type", "api_error",
			"filename", filename,
			"size_bytes", len(torrentBytes),
		)

		return nil, err
	}

	logger.InfoContext(ctx, "transfer created from metainfo", "transfer_id", torrent.ID, "name", torrent.Name)

	return torrent, nil
}

func (h *TransmissionHandler) handleTorrentAdd(ctx context.Context, req *TransmissionRequest) (*TransmissionResponse, error) {
	logger := logctx.LoggerFromContext(ctx).With("method", "handle_torrent_add")

	torrent, err := h.resolveTorrent(ctx, req, logger)
	if err != nil {
		return nil, err
	}

	if h.seedCriteria != nil {
		hash, err := transmissionHash(req, torrent.ID)
		if err != nil {
			return nil, err
		}

		if err := h.seedCriteria.AssociateTorrentHash(hash, torrent.ID); err != nil {
			return nil, fmt.Errorf("associate torrent hash with Put.io transfer: %w", err)
		}
	}

	// Marshal success response (Transmission format with torrent-added)
	hash, err := transmissionHash(req, torrent.ID)
	if err != nil {
		return nil, err
	}

	jsonTorrent, err := json.Marshal(map[string]interface{}{
		"torrent-added": map[string]interface{}{
			"id":         torrent.ID,
			"name":       torrent.Name,
			"hashString": hash,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to marshal torrent: %w", err)
	}

	return &TransmissionResponse{
		Result:    "success",
		Arguments: jsonTorrent,
	}, nil
}

func transmissionHash(req *TransmissionRequest, transferID string) (string, error) {
	if req.Arguments.MetaInfo != "" {
		return infoHashFromMetainfo(req.Arguments.MetaInfo)
	}

	if hash := infoHashFromMagnet(req.Arguments.FileName); hash != "" {
		return hash, nil
	}

	return syntheticTorrentHash(transferID), nil
}

func infoHashFromMetainfo(encoded string) (string, error) {
	metainfo, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode torrent metainfo for infohash: %w", err)
	}

	var torrent map[string]bencode.RawMessage
	if err := bencode.DecodeBytes(metainfo, &torrent); err != nil {
		return "", fmt.Errorf("decode torrent metainfo for infohash: %w", err)
	}

	info, ok := torrent["info"]
	if !ok {
		return "", fmt.Errorf("torrent metainfo has no info dictionary")
	}

	sum := sha1.Sum(info)

	return hex.EncodeToString(sum[:]), nil
}

func infoHashFromMagnet(magnet string) string {
	parsed, err := url.Parse(magnet)
	if err != nil {
		return ""
	}

	for _, xt := range parsed.Query()["xt"] {
		if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
			continue
		}

		value := xt[len("urn:btih:"):]

		if len(value) == sha1.Size*2 {
			_, decodeErr := hex.DecodeString(value)
			if decodeErr == nil {
				return strings.ToLower(value)
			}
		}

		if len(value) == 32 {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
			if err == nil && len(decoded) == sha1.Size {
				return hex.EncodeToString(decoded)
			}
		}
	}

	return ""
}

func syntheticTorrentHash(transferID string) string {
	sum := sha1.Sum([]byte(transferID))

	return hex.EncodeToString(sum[:])
}

func (h *TransmissionHandler) handleTorrentSet(ctx context.Context, req *TransmissionRequest) (*TransmissionResponse, error) {
	if h.seedCriteria == nil {
		return nil, fmt.Errorf("torrent seed criteria storage is unavailable")
	}

	if len(req.Arguments.IDs) == 0 {
		return nil, fmt.Errorf("torrent-set requires at least one torrent ID")
	}

	if err := h.ensureTorrentHashMappings(ctx, req.Arguments.IDs); err != nil {
		return nil, err
	}

	for _, hash := range req.Arguments.IDs {
		hash = strings.ToLower(hash)
		criteria := storage.TorrentSeedCriteria{
			SeedRatioLimit: req.Arguments.SeedRatioLimit,
			SeedRatioMode:  req.Arguments.SeedRatioMode,
			SeedIdleLimit:  req.Arguments.SeedIdleLimit,
			SeedIdleMode:   req.Arguments.SeedIdleMode,
		}

		if invalidSeedRatioLimit(criteria.SeedRatioLimit) {
			return nil, fmt.Errorf("seedRatioLimit must be a finite non-negative number")
		}

		if criteria.SeedRatioMode != nil && (*criteria.SeedRatioMode < 0 || *criteria.SeedRatioMode > 2) {
			return nil, fmt.Errorf("seedRatioMode must be 0, 1, or 2")
		}

		if criteria.SeedIdleLimit != nil && *criteria.SeedIdleLimit < 0 {
			return nil, fmt.Errorf("seedIdleLimit must be non-negative")
		}

		if criteria.SeedIdleMode != nil && (*criteria.SeedIdleMode < 0 || *criteria.SeedIdleMode > 2) {
			return nil, fmt.Errorf("seedIdleMode must be 0, 1, or 2")
		}

		if err := h.seedCriteria.UpdateTorrentSeedCriteria(hash, criteria); err != nil {
			return nil, fmt.Errorf("persist seed criteria for torrent %s: %w", hash, err)
		}
	}

	return &TransmissionResponse{Result: "success"}, nil
}

func (h *TransmissionHandler) handleTorrentStart(ctx context.Context, req *TransmissionRequest) (*TransmissionResponse, error) {
	return h.updateTorrentState(ctx, req.Arguments.IDs, false)
}

func (h *TransmissionHandler) handleTorrentStop(ctx context.Context, req *TransmissionRequest) (*TransmissionResponse, error) {
	return h.updateTorrentState(ctx, req.Arguments.IDs, true)
}

func (h *TransmissionHandler) updateTorrentState(ctx context.Context, hashes []string, stopped bool) (*TransmissionResponse, error) {
	if h.seedCriteria == nil {
		return nil, fmt.Errorf("torrent state storage is unavailable")
	}

	if len(hashes) == 0 {
		return nil, fmt.Errorf("torrent state update requires at least one torrent ID")
	}

	if err := h.ensureTorrentHashMappings(ctx, hashes); err != nil {
		return nil, err
	}

	for _, hash := range hashes {
		criteria := storage.TorrentSeedCriteria{Stopped: &stopped}
		if err := h.seedCriteria.UpdateTorrentSeedCriteria(strings.ToLower(hash), criteria); err != nil {
			return nil, fmt.Errorf("persist state for torrent %s: %w", hash, err)
		}
	}

	return &TransmissionResponse{Result: "success"}, nil
}

func invalidSeedRatioLimit(limit *float64) bool {
	return limit != nil && (*limit < 0 || math.IsNaN(*limit) || math.IsInf(*limit, 0))
}

// resolveTorrent determines the torrent source (metainfo or magnet) and adds the transfer.
func (h *TransmissionHandler) resolveTorrent(
	ctx context.Context, req *TransmissionRequest, logger *slog.Logger,
) (*transfer.Transfer, error) {
	// Requirement API-06: Prioritize MetaInfo when both present
	if req.Arguments.MetaInfo != "" {
		// Requirement API-01: Detect MetaInfo field
		logger.DebugContext(ctx, "processing torrent add request", "torrent_type", "metainfo")

		if h.telemetry != nil {
			h.telemetry.RecordTorrentType(ctx, "metainfo")
		}

		return h.handleTorrentAddByMetaInfo(ctx, req)
	}

	if req.Arguments.FileName != "" {
		// Requirement API-05: Maintain backward compatibility
		logger.DebugContext(ctx, "processing torrent add request", "torrent_type", "magnet")

		if h.telemetry != nil {
			h.telemetry.RecordTorrentType(ctx, "magnet")
		}

		magnetLink := req.Arguments.FileName
		// we use the label as the download directory. When using put.io as a shared download client,
		// we can't use the download directory from the request.
		torrent, err := h.dc.AddTransfer(ctx, magnetLink, h.label)
		if err != nil {
			return nil, fmt.Errorf("failed to add transfer: %w", err)
		}

		return torrent, nil
	}

	return nil, fmt.Errorf("either metainfo or filename must be provided")
}

func (h *TransmissionHandler) handleTorrentRemove(ctx context.Context, req *TransmissionRequest) (*TransmissionResponse, error) {
	logger := logctx.LoggerFromContext(ctx)
	logger.DebugContext(ctx, "received torrent remove request")

	transferIDs := req.Arguments.IDs

	if h.seedCriteria != nil {
		var err error
		transferIDs, err = h.resolveTransferIDs(ctx, req.Arguments.IDs)

		if err != nil {
			return nil, err
		}
	}

	if len(transferIDs) == 0 {
		return &TransmissionResponse{Result: "success"}, nil
	}

	err := h.dc.RemoveTransfers(ctx, transferIDs, req.Arguments.DeleteLocalData)
	if err != nil {
		return nil, fmt.Errorf("failed to remove transfers: %w", err)
	}

	if h.seedCriteria != nil {
		for _, hash := range req.Arguments.IDs {
			if err := h.seedCriteria.DeleteTorrentSeedCriteria(strings.ToLower(hash)); err != nil {
				logger.WarnContext(ctx, "failed to delete torrent seed criteria", "hash", hash, "err", err)
			}
		}
	}

	return &TransmissionResponse{
		Result: "success",
	}, nil
}

func (h *TransmissionHandler) resolveTransferIDs(ctx context.Context, hashes []string) ([]string, error) {
	if err := h.ensureTorrentHashMappings(ctx, hashes); err != nil {
		return nil, err
	}

	transfers, err := h.dc.GetTaggedTorrents(ctx, h.label)
	if err != nil {
		return nil, fmt.Errorf("list transfers to resolve torrent hashes: %w", err)
	}

	transferIDs := make([]string, 0, len(hashes))

	for _, hash := range hashes {
		transferID, err := h.seedCriteria.ResolveTorrentID(strings.ToLower(hash))
		if err != nil {
			return nil, fmt.Errorf("resolve transfer for torrent %s: %w", hash, err)
		}

		if transferID == "" {
			transferID = findTransferByHash(transfers, hash)
		}

		if transferID == "" {
			return nil, fmt.Errorf("torrent %s not found", hash)
		}

		transferIDs = append(transferIDs, syntheticTorrentHash(transferID))
	}

	return transferIDs, nil
}

func (h *TransmissionHandler) ensureTorrentHashMappings(ctx context.Context, hashes []string) error {
	transfers, err := h.dc.GetTaggedTorrents(ctx, h.label)
	if err != nil {
		return fmt.Errorf("list transfers to resolve torrent hashes: %w", err)
	}

	for _, hash := range hashes {
		resolved, err := h.seedCriteria.ResolveTorrentID(strings.ToLower(hash))
		if err != nil {
			return fmt.Errorf("resolve transfer for torrent %s: %w", hash, err)
		}

		if resolved != "" {
			continue
		}

		resolved = findTransferByHash(transfers, hash)
		if resolved == "" {
			return fmt.Errorf("torrent %s not found", hash)
		}

		if err := h.seedCriteria.AssociateTorrentHash(strings.ToLower(hash), resolved); err != nil {
			return fmt.Errorf("associate torrent %s with transfer: %w", hash, err)
		}
	}

	return nil
}

func findTransferByHash(transfers []*transfer.Transfer, hash string) string {
	for _, item := range transfers {
		if item.ID == hash || strings.EqualFold(syntheticTorrentHash(item.ID), hash) || strings.EqualFold(infoHashFromMagnet(item.MagnetURI), hash) {
			return item.ID
		}
	}

	return ""
}

// shouldGateLocalImport reports whether Put.io considers the remote transfer done enough
// that *arr must wait for the local copy before importing.
func shouldGateLocalImport(status string, transfer *transfer.Transfer) bool {
	switch strings.ToLower(status) {
	case "downloading", "in_queue", "waiting":
		return false
	case "completed", "finished", "seeding", "seedingwait", "completing":
		return true
	default:
		return transfer.Size > 0 && transfer.Downloaded >= transfer.Size
	}
}

// computeLocalDownloadedBytes delegates to transfer.LocalDownloadedBytes. It exists as a
// thin local wrapper because buildTransmissionTorrent shadows the transfer package name
// with a parameter, preventing a direct package-qualified call there.
func computeLocalDownloadedBytes(downloadDir string, t *transfer.Transfer) int64 {
	return transfer.LocalDownloadedBytes(downloadDir, t)
}

func localDownloadStatusMap(records []storage.DownloadRecord) map[string]string {
	statuses := make(map[string]string, len(records))
	for _, record := range records {
		statuses[record.DownloadID] = record.Status
	}

	return statuses
}

//nolint:cyclop,funlen,gocyclo // Transmission status conversion has independent state and import gates.
func (h *TransmissionHandler) buildTransmissionTorrent(
	ctx context.Context,
	transfer *transfer.Transfer,
	localStatus string,
	criteria storage.TorrentSeedCriteria,
) (TransmissionTorrent, error) {
	logger := logctx.LoggerFromContext(ctx)

	id, err := strconv.ParseInt(transfer.ID, 10, 64)
	if err != nil {
		return TransmissionTorrent{}, fmt.Errorf("parse transfer ID %q: %w", transfer.ID, err)
	}

	var status TransmissionTorrentStatus

	var errorString *string

	downloadedEver := transfer.Downloaded
	leftUntilDone := transfer.Size - transfer.Downloaded
	isFinished := strings.ToLower(transfer.Status) == "completed" ||
		strings.ToLower(transfer.Status) == "seeding" ||
		strings.ToLower(transfer.Status) == "finished"

	switch strings.ToLower(transfer.Status) {
	case "downloading":
		status = StatusDownload
	case "in_queue", "waiting":
		status = StatusDownloadWait
	case "finishing", "checking", "completing":
		status = StatusCheck
	case "completed", "finished":
		status = StatusSeed
	case "seeding":
		status = StatusSeed
	case "seedingwait":
		status = StatusSeedWait
	case "error":
		status = StatusStopped

		if transfer.ErrorMessage != "" {
			errorString = &transfer.ErrorMessage
		}
	default:
		logger.WarnContext(ctx, "unknown put.io transfer status, defaulting to stopped",
			"status", transfer.Status, "transfer_id", transfer.ID)

		status = StatusStopped
	}

	// Gate *arr import on local disk progress whenever Put.io reports complete but the
	// local copy is not finished. Do not depend solely on trackLocal (DB load): a broken
	// GetDownloads scan would leave trackLocal false and Radarr/Sonarr would import partial files.
	if shouldGateLocalImport(transfer.Status, transfer) && localStatus != "downloaded" && h.localDownloadDir != "" {
		localDownloaded := computeLocalDownloadedBytes(h.localDownloadDir, transfer)
		if localDownloaded > transfer.Size {
			localDownloaded = transfer.Size
		}

		downloadedEver = localDownloaded
		leftUntilDone = transfer.Size - localDownloaded
		isFinished = false

		switch localStatus {
		case "failed", "missing":
			status = StatusStopped

			msg := "local download failed"
			if localStatus == "missing" {
				msg = "transfer files missing from Put.io"
			}

			errorString = &msg
		default:
			status = StatusDownload
		}
	}

	hashBytes := sha1.Sum([]byte(transfer.ID))
	seedRatioLimit, seedRatioMode := seedRatioCriteria(criteria)
	seedIdleLimit, seedIdleMode := seedIdleCriteria(criteria)

	if criteria.Stopped != nil && *criteria.Stopped {
		status = StatusStopped
	}

	if seedGoalReached(transfer, status, seedRatioLimit, seedRatioMode, seedIdleLimit, seedIdleMode) {
		status = StatusStopped
	}

	return TransmissionTorrent{
		ID:                 id,
		HashString:         hex.EncodeToString(hashBytes[:]),
		Name:               transfer.Name,
		DownloadDir:        transfer.SavePath,
		TotalSize:          transfer.Size,
		LeftUntilDone:      leftUntilDone,
		IsFinished:         isFinished,
		ETA:                transfer.EstimatedTime,
		Status:             status,
		ErrorString:        errorString,
		DownloadedEver:     downloadedEver,
		UploadedEver:       transfer.Uploaded,
		UploadRatio:        uploadRatio(transfer.Uploaded, transfer.Downloaded),
		SecondsSeeding:     transfer.SecondsSeeding,
		Labels:             []string{h.label},
		PeersConnected:     transfer.PeersConnected,
		PeersSendingToUs:   transfer.PeersSendingToUs,
		PeersGettingFromUs: transfer.PeersGettingFromUs,
		RateDownload:       transfer.DownloadSpeed,
		FileCount:          uint32(len(transfer.Files)),
		SeedRatioLimit:     seedRatioLimit,
		SeedRatioMode:      uint32(seedRatioMode),
		SeedIdleLimit:      uint64(seedIdleLimit),
		SeedIdleMode:       uint32(seedIdleMode),
	}, nil
}

func seedGoalReached(t *transfer.Transfer, status TransmissionTorrentStatus, ratioLimit float32, ratioMode int64, idleLimit, idleMode int64) bool {
	if status != StatusSeed && status != StatusSeedWait {
		return false
	}

	ratio := uploadRatio(t.Uploaded, t.Downloaded)
	ratioReached := ratioMode == 1 && ratio >= float64(ratioLimit) || ratioMode == 0 && ratio >= 1
	idleReached := idleMode == 1 && t.SecondsSeeding > idleLimit*60

	return ratioReached || idleReached
}

func seedRatioCriteria(criteria storage.TorrentSeedCriteria) (float32, int64) {
	limit, mode := float32(defaultSeedRatioLimit), int64(1)
	if criteria.SeedRatioLimit != nil {
		limit = float32(*criteria.SeedRatioLimit)
	}

	if criteria.SeedRatioMode != nil {
		mode = *criteria.SeedRatioMode
	}

	return limit, mode
}

func seedIdleCriteria(criteria storage.TorrentSeedCriteria) (int64, int64) {
	limit, mode := int64(defaultSeedTimeMinutes), int64(1)
	if criteria.SeedIdleLimit != nil {
		limit = *criteria.SeedIdleLimit
	}

	if criteria.SeedIdleMode != nil {
		mode = *criteria.SeedIdleMode
	}

	return limit, mode
}

func uploadRatio(uploaded, downloaded int64) float64 {
	if downloaded <= 0 {
		return 0
	}

	return float64(uploaded) / float64(downloaded)
}

func (h *TransmissionHandler) handleTorrentGet(ctx context.Context) (*TransmissionResponse, error) {
	logger := logctx.LoggerFromContext(ctx).With("method", "handle_torrent_get")

	logger.DebugContext(ctx, "fetching torrents from download client")

	transfers, err := h.dc.GetTaggedTorrents(ctx, h.label)
	if err != nil {
		return nil, fmt.Errorf("failed to get torrents: %w", err)
	}

	torrentsCount := len(transfers)
	logger.DebugContext(ctx, "fetched torrents from download client", "count", torrentsCount)

	localStatuses := map[string]string{}

	if h.localDownloads != nil {
		records, err := h.localDownloads.GetDownloads()
		if err != nil {
			logger.WarnContext(ctx, "failed to load local download state, using disk progress only", "err", err)
		} else {
			localStatuses = localDownloadStatusMap(records)
		}
	}

	logger.DebugContext(ctx, "converting torrents to transmission format")

	transmissionTorrents := make([]TransmissionTorrent, 0, torrentsCount)

	for _, transfer := range transfers {
		hash := syntheticTorrentHash(transfer.ID)
		if magnetHash := infoHashFromMagnet(transfer.MagnetURI); magnetHash != "" {
			hash = magnetHash
		}

		if h.seedCriteria != nil {
			if mappedHash, mapErr := h.seedCriteria.GetTorrentHash(transfer.ID); mapErr != nil {
				logger.WarnContext(ctx, "failed to load torrent hash", "transfer_id", transfer.ID, "err", mapErr)
			} else if mappedHash != "" {
				hash = mappedHash
			}
		}

		var criteria storage.TorrentSeedCriteria

		if h.seedCriteria != nil {
			criteria, err = h.seedCriteria.GetTorrentSeedCriteria(transfer.ID, hash)
			if err != nil {
				logger.WarnContext(ctx, "failed to load torrent seed criteria", "hash", hash, "err", err)

				criteria = storage.TorrentSeedCriteria{}
			}

			if criteria.Stopped == nil {
				stopped := false
				criteria.Stopped = &stopped
			}
		}

		torrent, err := h.buildTransmissionTorrent(ctx, transfer, localStatuses[transfer.ID], criteria)
		if err != nil {
			logger.ErrorContext(ctx, "failed to build transmission torrent", "transfer_id", transfer.ID, "err", err)

			continue
		}

		torrent.HashString = hash

		transmissionTorrents = append(transmissionTorrents, torrent)
	}

	logger.DebugContext(ctx, "converted torrents to transmission format", "count", len(transmissionTorrents))

	jsonTorrents, err := json.Marshal(map[string]interface{}{
		"torrents": transmissionTorrents,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal torrents: %w", err)
	}

	return &TransmissionResponse{
		Result:    "success",
		Arguments: jsonTorrents,
	}, nil
}

// formatTransmissionError converts internal errors to Transmission-compatible error messages.
// Transmission RPC uses the "result" field for error reporting - this function
// produces user-friendly error messages for common failure cases.
func formatTransmissionError(err error) string {
	var invalidErr *transfer.InvalidContentError
	if errors.As(err, &invalidErr) {
		return fmt.Sprintf("invalid torrent: %s", invalidErr.Reason)
	}

	var networkErr *transfer.NetworkError
	if errors.As(err, &networkErr) {
		return fmt.Sprintf("upload failed: %s", networkErr.APIMessage)
	}

	var dirErr *transfer.DirectoryError
	if errors.As(err, &dirErr) {
		return fmt.Sprintf("directory error: %s", dirErr.Reason)
	}

	var authErr *transfer.AuthenticationError
	if errors.As(err, &authErr) {
		return "authentication failed"
	}

	// Generic fallback for unknown errors
	return fmt.Sprintf("error: %v", err)
}
