package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/italolelis/seedbox_downloader/internal/http/rest"
	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

const (
	testUser = "admin"
	testPass = "secret"
)

type fakeLister struct{ transfers []*transfer.Transfer }

func (f *fakeLister) GetTaggedTorrents(_ context.Context, _ string) ([]*transfer.Transfer, error) {
	return f.transfers, nil
}

type fakeRepo struct {
	records []storage.DownloadRecord
	reset   bool
}

func (f *fakeRepo) GetDownloads() ([]storage.DownloadRecord, error) { return f.records, nil }
func (f *fakeRepo) GetByTransferID(string) (storage.DownloadRecord, error) {
	return storage.DownloadRecord{}, storage.ErrNotFound
}
func (f *fakeRepo) UpdateTransferStatus(string, string) error { return nil }
func (f *fakeRepo) DeleteByTransferID(string) error           { return nil }
func (f *fakeRepo) ResetDownloads() error                     { f.reset = true; return nil }

type fakeRequeuer struct{}

func (fakeRequeuer) Requeue(context.Context, string) error { return nil }

type fakeCanceller struct{}

func (fakeCanceller) CancelDownload(string) bool { return true }

type fakeRemover struct{}

func (fakeRemover) RemoveTransfers(context.Context, []string, bool) error { return nil }

func newTestRouter(repo *fakeRepo, lister *fakeLister) http.Handler {
	svc := transfers.NewService(lister, repo, fakeRequeuer{}, fakeCanceller{}, fakeRemover{}, "tv-sonarr", "")
	handler := rest.NewUIHandler(svc, rest.ConfigSnapshot{Version: "test"}, []byte("test-secret"))

	r := chi.NewRouter()
	r.Use(rest.BasicAuth(testUser, testPass))
	r.Mount("/", handler.Routes())

	return r
}

func TestUIRequiresAuth(t *testing.T) {
	t.Parallel()

	router := newTestRouter(&fakeRepo{}, &fakeLister{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rec.Code)
	}
}

func TestUIHealthWithAuth(t *testing.T) {
	t.Parallel()

	router := newTestRouter(&fakeRepo{}, &fakeLister{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.SetBasicAuth(testUser, testPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with auth, got %d", rec.Code)
	}
}

func TestUIListTransfers(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{transfers: []*transfer.Transfer{{ID: "1", Name: "Show", Status: "downloading"}}}
	router := newTestRouter(&fakeRepo{}, lister)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transfers", nil)
	req.SetBasicAuth(testUser, testPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Transfers []transfers.TransferView `json:"transfers"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if len(body.Transfers) != 1 || body.Transfers[0].ID != "1" {
		t.Fatalf("unexpected transfers: %+v", body.Transfers)
	}
}

func TestUIDestructiveRequiresConfirmToken(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	router := newTestRouter(repo, &fakeLister{})

	// Without a confirm token, the reset must be rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/db/reset", nil)
	req.SetBasicAuth(testUser, testPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without confirm token, got %d", rec.Code)
	}

	if repo.reset {
		t.Fatal("database must not be reset without a confirm token")
	}

	// Obtain a confirm token.
	tokenReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/confirm-token", nil)
	tokenReq.SetBasicAuth(testUser, testPass)
	tokenRec := httptest.NewRecorder()
	router.ServeHTTP(tokenRec, tokenReq)

	var tokenBody struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(tokenRec.Body).Decode(&tokenBody); err != nil {
		t.Fatalf("failed to decode token: %v", err)
	}

	// With a valid confirm token, the reset succeeds.
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/db/reset", nil)
	okReq.SetBasicAuth(testUser, testPass)
	okReq.Header.Set("X-Confirm-Token", tokenBody.Token)
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 with confirm token, got %d", okRec.Code)
	}

	if !repo.reset {
		t.Fatal("expected database reset with valid confirm token")
	}
}
