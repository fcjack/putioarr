// Package transfers provides the read model and action use cases that back the
// putioarr Web UI. It composes the existing Put.io client, SQLite repository,
// transfer orchestrator and downloader without duplicating their business rules.
package transfers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

// ErrNotFound is returned when a transfer cannot be found on Put.io or in SQLite.
var ErrNotFound = errors.New("transfer not found")

// TransferLister lists tagged transfers from the download client (Put.io).
type TransferLister interface {
	GetTaggedTorrents(ctx context.Context, label string) ([]*transfer.Transfer, error)
}

// Repository is the subset of storage operations the UI service needs.
type Repository interface {
	GetDownloads() ([]storage.DownloadRecord, error)
	GetByTransferID(transferID string) (storage.DownloadRecord, error)
	UpdateTransferStatus(transferID, status string) error
	DeleteByTransferID(transferID string) error
	ResetDownloads() error
}

// Requeuer re-enqueues a transfer for local download (satisfied by the orchestrator).
type Requeuer interface {
	Requeue(ctx context.Context, transferID string) error
}

// Canceller cancels an in-flight local download (satisfied by the downloader).
type Canceller interface {
	CancelDownload(transferID string) bool
}

// PutioRemover removes transfers from Put.io (satisfied by the Put.io client).
type PutioRemover interface {
	RemoveTransfers(ctx context.Context, transferIDs []string, deleteLocalData bool) error
}

// Service builds UI read models and executes UI actions.
type Service struct {
	lister       TransferLister
	repo         Repository
	requeuer     Requeuer
	canceller    Canceller
	putioRemover PutioRemover
	label        string
	downloadDir  string
}

// NewService creates a UI transfers service.
func NewService(
	lister TransferLister,
	repo Repository,
	requeuer Requeuer,
	canceller Canceller,
	putioRemover PutioRemover,
	label string,
	downloadDir string,
) *Service {
	return &Service{
		lister:       lister,
		repo:         repo,
		requeuer:     requeuer,
		canceller:    canceller,
		putioRemover: putioRemover,
		label:        label,
		downloadDir:  downloadDir,
	}
}

// FileView is a single file inside a transfer.
type FileView struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// TransferView is the composite read model for a single transfer in the list.
type TransferView struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Label           string  `json:"label"`
	Status          string  `json:"status"`
	PutioStatus     string  `json:"putioStatus"`
	LocalStatus     string  `json:"localStatus,omitempty"`
	Size            int64   `json:"size"`
	Downloaded      int64   `json:"downloaded"`
	LocalDownloaded int64   `json:"localDownloaded"`
	Progress        float64 `json:"progress"`
	DownloadSpeed   int64   `json:"downloadSpeed"`
	ETA             int64   `json:"eta"`
	SavePath        string  `json:"savePath,omitempty"`
	ErrorMessage    string  `json:"errorMessage,omitempty"`
	ExistsOnPutio   bool    `json:"existsOnPutio"`
	DownloadedAt    string  `json:"downloadedAt,omitempty"`
	FileCount       int     `json:"fileCount"`
}

// TransferDetail extends TransferView with files and a derived timeline.
type TransferDetail struct {
	TransferView

	LocalPaths []string        `json:"localPaths"`
	Files      []FileView      `json:"files"`
	Timeline   []TimelineEvent `json:"timeline"`
}

// ListFilters narrows the transfer list.
type ListFilters struct {
	Name   string
	Status string
	Label  string
}

// List returns the composite view of all transfers, merging Put.io, SQLite and disk state.
func (s *Service) List(ctx context.Context, filters ListFilters) ([]TransferView, error) {
	transfers, err := s.lister.GetTaggedTorrents(ctx, s.label)
	if err != nil {
		return nil, fmt.Errorf("failed to list transfers: %w", err)
	}

	records := s.loadRecords(ctx)

	views := make([]TransferView, 0, len(transfers))
	seen := make(map[string]struct{}, len(transfers))

	for _, t := range transfers {
		seen[t.ID] = struct{}{}

		views = append(views, s.buildView(t, records[t.ID]))
	}

	// Include SQLite rows with no matching Put.io transfer (orphaned / cleaned up).
	for id, record := range records {
		if _, ok := seen[id]; ok {
			continue
		}

		views = append(views, s.buildOrphanView(record))
	}

	views = filterViews(views, filters)

	sort.Slice(views, func(i, j int) bool {
		return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name)
	})

	return views, nil
}

// Get returns the detailed view of a single transfer.
func (s *Service) Get(ctx context.Context, id string) (*TransferDetail, error) {
	transfers, err := s.lister.GetTaggedTorrents(ctx, s.label)
	if err != nil {
		return nil, fmt.Errorf("failed to list transfers: %w", err)
	}

	record := s.recordOrEmpty(ctx, id)

	for _, t := range transfers {
		if t.ID == id {
			return s.buildDetail(t, record), nil
		}
	}

	// Not on Put.io but tracked in SQLite -> orphaned detail.
	if _, err := s.repo.GetByTransferID(id); err == nil {
		view := s.buildOrphanView(record)

		return &TransferDetail{
			TransferView: view,
			LocalPaths:   []string{},
			Files:        []FileView{},
			Timeline:     buildTimeline(view),
		}, nil
	}

	return nil, ErrNotFound
}

func (s *Service) loadRecords(ctx context.Context) map[string]storage.DownloadRecord {
	records, err := s.repo.GetDownloads()
	if err != nil {
		return map[string]storage.DownloadRecord{}
	}

	byID := make(map[string]storage.DownloadRecord, len(records))
	for _, r := range records {
		byID[r.DownloadID] = r
	}

	return byID
}

func (s *Service) recordOrEmpty(ctx context.Context, id string) storage.DownloadRecord {
	record, err := s.repo.GetByTransferID(id)
	if err != nil {
		return storage.DownloadRecord{}
	}

	return record
}

func (s *Service) buildView(t *transfer.Transfer, record storage.DownloadRecord) TransferView {
	localDownloaded := transfer.LocalDownloadedBytes(s.downloadDir, t)
	status := transfer.ComputeStatus(t.Status, record.Status, t.Size, localDownloaded, true)

	downloaded := t.Downloaded
	if localDownloaded > 0 {
		downloaded = localDownloaded
	}

	return TransferView{
		ID:              t.ID,
		Name:            t.Name,
		Label:           t.Label,
		Status:          string(status),
		PutioStatus:     t.Status,
		LocalStatus:     record.Status,
		Size:            t.Size,
		Downloaded:      downloaded,
		LocalDownloaded: localDownloaded,
		Progress:        progressPercent(downloaded, t.Size),
		DownloadSpeed:   t.DownloadSpeed,
		ETA:             t.EstimatedTime,
		SavePath:        t.SavePath,
		ErrorMessage:    t.ErrorMessage,
		ExistsOnPutio:   true,
		DownloadedAt:    record.DownloadedAt,
		FileCount:       len(t.Files),
	}
}

func (s *Service) buildOrphanView(record storage.DownloadRecord) TransferView {
	status := transfer.ComputeStatus("", record.Status, 0, 0, false)

	// Prefer the persisted human-readable name; fall back to the ID for legacy
	// rows that were claimed before the name was stored.
	name := record.Name
	if name == "" {
		name = record.DownloadID
	}

	return TransferView{
		ID:            record.DownloadID,
		Name:          name,
		Label:         s.label,
		Status:        string(status),
		LocalStatus:   record.Status,
		ExistsOnPutio: false,
		DownloadedAt:  record.DownloadedAt,
	}
}

func (s *Service) buildDetail(t *transfer.Transfer, record storage.DownloadRecord) *TransferDetail {
	view := s.buildView(t, record)

	files := make([]FileView, 0, len(t.Files))
	paths := make([]string, 0, len(t.Files))

	for _, f := range t.Files {
		files = append(files, FileView{Path: f.Path, Size: f.Size})
		paths = append(paths, f.Path)
	}

	return &TransferDetail{
		TransferView: view,
		LocalPaths:   paths,
		Files:        files,
		Timeline:     buildTimeline(view),
	}
}

func progressPercent(downloaded, size int64) float64 {
	if size <= 0 {
		return 0
	}

	percent := float64(downloaded) * 100 / float64(size)
	if percent > 100 {
		percent = 100
	}

	return percent
}

func filterViews(views []TransferView, filters ListFilters) []TransferView {
	name := strings.ToLower(strings.TrimSpace(filters.Name))
	status := strings.ToLower(strings.TrimSpace(filters.Status))
	label := strings.ToLower(strings.TrimSpace(filters.Label))

	filtered := make([]TransferView, 0, len(views))

	for _, v := range views {
		if name != "" && !strings.Contains(strings.ToLower(v.Name), name) {
			continue
		}

		if status != "" && strings.ToLower(v.Status) != status {
			continue
		}

		if label != "" && strings.ToLower(v.Label) != label {
			continue
		}

		filtered = append(filtered, v)
	}

	return filtered
}
