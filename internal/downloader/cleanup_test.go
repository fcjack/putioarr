package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/svc/arr"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

// arrServer returns an httptest server emulating an *arr history endpoint that
// reports the given dropped paths as imported via downloadFolderImported events.
func arrServer(t *testing.T, droppedPaths ...string) *httptest.Server {
	t.Helper()

	type record struct {
		EventType string                 `json:"eventType"`
		Data      map[string]interface{} `json:"data"`
	}

	type response struct {
		Records      []record `json:"records"`
		TotalRecords int      `json:"totalRecords"`
	}

	records := make([]record, 0, len(droppedPaths))
	for _, p := range droppedPaths {
		records = append(records, record{
			EventType: "downloadFolderImported",
			Data:      map[string]interface{}{"droppedPath": p},
		})
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(response{Records: records, TotalRecords: len(records)})
	}))
}

func newTestDownloader(t *testing.T, downloadDir string, cleanup CleanupOptions, services ...*arr.Client) *Downloader {
	t.Helper()

	return NewDownloader(downloadDir, 1, nil, nil, services, cleanup)
}

func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestCleanupAfterImport_FolderRelease(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	releaseName := "Show.S01.1080p.WEB-DL"
	releaseRoot := filepath.Join(downloadDir, releaseName)

	writeFile(t, filepath.Join(releaseRoot, "Show.S01E01.mkv"))
	writeFile(t, filepath.Join(releaseRoot, "Show.S01E02.mkv"))
	writeFile(t, filepath.Join(releaseRoot, "Show.S01E02.nfo"))

	srv := arrServer(t, releaseRoot)
	defer srv.Close()

	d := newTestDownloader(t, downloadDir,
		CleanupOptions{AfterImport: true, RemoveEmptyDirs: true},
		arr.NewClient("sonarr", "k", srv.URL))

	imported, err := d.cleanupAfterImport(context.Background(), &transfer.Transfer{ID: "1", Name: releaseName})
	if err != nil {
		t.Fatalf("cleanupAfterImport: %v", err)
	}

	if !imported {
		t.Fatal("expected transfer to be reported imported")
	}

	if _, err := os.Stat(releaseRoot); !os.IsNotExist(err) {
		t.Fatalf("expected release root removed, stat err = %v", err)
	}
}

func TestCleanupAfterImport_SingleFilePrunesEmptyParents(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	// Nested category dir to verify empty-parent pruning stops at downloadDir.
	releaseName := filepath.Join("tv", "Show.S01E01.1080p.mkv")
	releaseRoot := filepath.Join(downloadDir, releaseName)

	writeFile(t, releaseRoot)

	srv := arrServer(t, releaseRoot)
	defer srv.Close()

	d := newTestDownloader(t, downloadDir,
		CleanupOptions{AfterImport: true, RemoveEmptyDirs: true},
		arr.NewClient("radarr", "k", srv.URL))

	imported, err := d.cleanupAfterImport(context.Background(), &transfer.Transfer{ID: "2", Name: releaseName})
	if err != nil {
		t.Fatalf("cleanupAfterImport: %v", err)
	}

	if !imported {
		t.Fatal("expected transfer to be reported imported")
	}

	if _, err := os.Stat(releaseRoot); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err = %v", err)
	}

	if _, err := os.Stat(filepath.Join(downloadDir, "tv")); !os.IsNotExist(err) {
		t.Fatal("expected empty parent directory pruned")
	}

	if _, err := os.Stat(downloadDir); err != nil {
		t.Fatalf("DOWNLOAD_DIR must never be removed: %v", err)
	}
}

func TestCleanupAfterImport_NotImportedKeepsFiles(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	releaseName := "Show.S01.1080p.WEB-DL"
	releaseRoot := filepath.Join(downloadDir, releaseName)
	file := filepath.Join(releaseRoot, "Show.S01E01.mkv")

	writeFile(t, file)

	// Server reports an unrelated release as imported.
	srv := arrServer(t, filepath.Join(downloadDir, "Unrelated", "ep.mkv"))
	defer srv.Close()

	d := newTestDownloader(t, downloadDir,
		CleanupOptions{AfterImport: true, RemoveEmptyDirs: true},
		arr.NewClient("sonarr", "k", srv.URL))

	imported, err := d.cleanupAfterImport(context.Background(), &transfer.Transfer{ID: "3", Name: releaseName})
	if err != nil {
		t.Fatalf("cleanupAfterImport: %v", err)
	}

	if imported {
		t.Fatal("expected transfer not imported")
	}

	if _, err := os.Stat(file); err != nil {
		t.Fatalf("files must be kept when not imported: %v", err)
	}
}

func TestCleanupAfterImport_DisabledKeepsFiles(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	releaseName := "Show.S01.1080p.WEB-DL"
	releaseRoot := filepath.Join(downloadDir, releaseName)
	file := filepath.Join(releaseRoot, "Show.S01E01.mkv")

	writeFile(t, file)

	srv := arrServer(t, releaseRoot)
	defer srv.Close()

	d := newTestDownloader(t, downloadDir,
		CleanupOptions{AfterImport: false, RemoveEmptyDirs: true},
		arr.NewClient("sonarr", "k", srv.URL))

	imported, err := d.cleanupAfterImport(context.Background(), &transfer.Transfer{ID: "4", Name: releaseName})
	if err != nil {
		t.Fatalf("cleanupAfterImport: %v", err)
	}

	if !imported {
		t.Fatal("expected import detection even when cleanup disabled")
	}

	if _, err := os.Stat(file); err != nil {
		t.Fatalf("files must be kept when cleanup disabled: %v", err)
	}
}

func TestCleanupAfterImport_SkipsUnconfiguredArr(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	releaseName := "Movie.2024.1080p.mkv"
	releaseRoot := filepath.Join(downloadDir, releaseName)

	writeFile(t, releaseRoot)

	srv := arrServer(t, releaseRoot)
	defer srv.Close()

	// An unconfigured client first must not short-circuit detection by the configured one.
	d := newTestDownloader(t, downloadDir,
		CleanupOptions{AfterImport: true, RemoveEmptyDirs: true},
		arr.NewClient("sonarr", "", ""),
		arr.NewClient("radarr", "k", srv.URL))

	imported, err := d.cleanupAfterImport(context.Background(), &transfer.Transfer{ID: "5", Name: releaseName})
	if err != nil {
		t.Fatalf("cleanupAfterImport: %v", err)
	}

	if !imported {
		t.Fatal("expected configured arr to confirm import despite unconfigured one")
	}
}

func TestRemoveEmptyParents_StopsAtNonEmptyAndDownloadDir(t *testing.T) {
	t.Parallel()

	downloadDir := t.TempDir()
	categoryDir := filepath.Join(downloadDir, "tv")
	releaseDir := filepath.Join(categoryDir, "Show.S01")
	siblingFile := filepath.Join(categoryDir, "keep.mkv")

	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeFile(t, siblingFile)

	d := newTestDownloader(t, downloadDir, CleanupOptions{AfterImport: true, RemoveEmptyDirs: true})

	d.removeEmptyParents(context.Background(), releaseDir)

	if _, err := os.Stat(releaseDir); !os.IsNotExist(err) {
		t.Fatal("expected empty release dir to be pruned")
	}

	if _, err := os.Stat(categoryDir); err != nil {
		t.Fatalf("non-empty parent must be kept: %v", err)
	}

	if _, err := os.Stat(siblingFile); err != nil {
		t.Fatalf("sibling file must be kept: %v", err)
	}
}
