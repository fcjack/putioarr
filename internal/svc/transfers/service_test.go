package transfers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

type mockLister struct {
	transfers []*transfer.Transfer
	err       error
}

func (m *mockLister) GetTaggedTorrents(_ context.Context, _ string) ([]*transfer.Transfer, error) {
	return m.transfers, m.err
}

type mockRepo struct {
	records  []storage.DownloadRecord
	byID     map[string]storage.DownloadRecord
	getErr   error
	deleted  []string
	resetHit bool
	statuses map[string]string
}

func (m *mockRepo) GetDownloads() ([]storage.DownloadRecord, error) { return m.records, nil }

func (m *mockRepo) GetByTransferID(id string) (storage.DownloadRecord, error) {
	if m.getErr != nil {
		return storage.DownloadRecord{}, m.getErr
	}

	if r, ok := m.byID[id]; ok {
		return r, nil
	}

	return storage.DownloadRecord{}, storage.ErrNotFound
}

func (m *mockRepo) UpdateTransferStatus(id, status string) error {
	if m.statuses == nil {
		m.statuses = map[string]string{}
	}

	m.statuses[id] = status

	return nil
}

func (m *mockRepo) DeleteByTransferID(id string) error {
	m.deleted = append(m.deleted, id)

	return nil
}

func (m *mockRepo) ResetDownloads() error {
	m.resetHit = true

	return nil
}

type mockRequeuer struct{ requeued []string }

func (m *mockRequeuer) Requeue(_ context.Context, id string) error {
	m.requeued = append(m.requeued, id)

	return nil
}

type mockCanceller struct{ cancelled []string }

func (m *mockCanceller) CancelDownload(id string) bool {
	m.cancelled = append(m.cancelled, id)

	return true
}

type mockPutioRemover struct {
	removed     [][]string
	deleteData  bool
	returnError error
}

func (m *mockPutioRemover) RemoveTransfers(_ context.Context, ids []string, deleteData bool) error {
	m.removed = append(m.removed, ids)
	m.deleteData = deleteData

	return m.returnError
}

func newService(lister *mockLister, repo *mockRepo) (*transfers.Service, *mockRequeuer, *mockCanceller, *mockPutioRemover) {
	requeuer := &mockRequeuer{}
	canceller := &mockCanceller{}
	remover := &mockPutioRemover{}
	svc := transfers.NewService(lister, repo, requeuer, canceller, remover, "tv-sonarr", "")

	return svc, requeuer, canceller, remover
}

func TestServiceList(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{
		{ID: "1", Name: "Beta", Status: "downloading", Size: 100, Downloaded: 50},
		{ID: "2", Name: "Alpha", Status: "completed", Size: 100, Downloaded: 100, Files: []*transfer.File{{Path: "Alpha", Size: 100}}},
	}}
	repo := &mockRepo{
		records: []storage.DownloadRecord{
			{DownloadID: "2", Status: "downloaded"},
			{DownloadID: "99", Status: "pending"}, // orphaned: no Put.io match
		},
		byID: map[string]storage.DownloadRecord{
			"2":  {DownloadID: "2", Status: "downloaded"},
			"99": {DownloadID: "99", Status: "pending"},
		},
	}

	svc, _, _, _ := newService(lister, repo)

	views, err := svc.List(context.Background(), transfers.ListFilters{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(views) != 3 {
		t.Fatalf("expected 3 views, got %d", len(views))
	}

	byID := map[string]transfers.TransferView{}

	var alphaIdx, betaIdx int

	for i, v := range views {
		byID[v.ID] = v

		switch v.ID {
		case "2":
			alphaIdx = i
		case "1":
			betaIdx = i
		}
	}

	// Views are sorted by name, so Alpha (id 2) precedes Beta (id 1).
	if alphaIdx > betaIdx {
		t.Errorf("expected Alpha before Beta, got indices alpha=%d beta=%d", alphaIdx, betaIdx)
	}

	if got := byID["1"].Status; got != string(transfer.StatusDownloadingOnPutio) {
		t.Errorf("transfer 1 status = %q, want %q", got, transfer.StatusDownloadingOnPutio)
	}

	if got := byID["2"].Status; got != string(transfer.StatusWaitingImport) {
		t.Errorf("transfer 2 status = %q, want %q", got, transfer.StatusWaitingImport)
	}

	if got := byID["99"].Status; got != string(transfer.StatusOrphaned) {
		t.Errorf("orphan 99 status = %q, want %q", got, transfer.StatusOrphaned)
	}
}

func TestServiceListFilters(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{
		{ID: "1", Name: "Breaking Bad", Status: "downloading"},
		{ID: "2", Name: "The Wire", Status: "completed"},
	}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{}}

	svc, _, _, _ := newService(lister, repo)

	views, err := svc.List(context.Background(), transfers.ListFilters{Name: "wire"})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(views) != 1 || views[0].ID != "2" {
		t.Fatalf("name filter failed, got %+v", views)
	}
}

func TestServiceGetNotFound(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{}}

	svc, _, _, _ := newService(lister, repo)

	_, err := svc.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, transfers.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestServiceGetDetail(t *testing.T) {
	t.Parallel()

	lister := &mockLister{transfers: []*transfer.Transfer{
		{ID: "1", Name: "Show", Status: "downloading", Size: 100, Downloaded: 30, Files: []*transfer.File{{Path: "Show/ep1.mkv", Size: 100}}},
	}}
	repo := &mockRepo{byID: map[string]storage.DownloadRecord{"1": {DownloadID: "1", Status: "downloading"}}}

	svc, _, _, _ := newService(lister, repo)

	detail, err := svc.Get(context.Background(), "1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	if len(detail.Files) != 1 || detail.Files[0].Path != "Show/ep1.mkv" {
		t.Errorf("unexpected files: %+v", detail.Files)
	}

	if len(detail.Timeline) == 0 {
		t.Error("expected non-empty timeline")
	}
}
