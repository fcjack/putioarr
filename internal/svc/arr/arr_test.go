package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newTestServer(t *testing.T, resp HistoryResponse) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestCheckImported(t *testing.T) {
	t.Parallel()

	downloadDir := "/downloads"

	tests := []struct {
		name        string
		releaseRoot string
		records     []HistoryRecord
		want        bool
	}{
		{
			name:        "single-file folder import exact match",
			releaseRoot: filepath.Join(downloadDir, "Show.S01E01.1080p.WEB-DL.mkv"),
			records: []HistoryRecord{
				{EventType: "downloadFolderImported", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "Show.S01E01.1080p.WEB-DL.mkv"),
				}},
			},
			want: true,
		},
		{
			name:        "single-file downloadImported event",
			releaseRoot: filepath.Join(downloadDir, "The.Movie.2024.1080p.mkv"),
			records: []HistoryRecord{
				{EventType: "downloadImported", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "The.Movie.2024.1080p.mkv"),
				}},
			},
			want: true,
		},
		{
			name:        "folder release: per-file import nested under release root",
			releaseRoot: filepath.Join(downloadDir, "Show.S01.1080p.WEB-DL"),
			records: []HistoryRecord{
				{EventType: "downloadFolderImported", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "Show.S01.1080p.WEB-DL", "Show.S01E01.mkv"),
				}},
			},
			want: true,
		},
		{
			name:        "folder release: root matched via importedPath fallback",
			releaseRoot: filepath.Join(downloadDir, "Show.S02.1080p.WEB-DL"),
			records: []HistoryRecord{
				{EventType: "downloadFolderImported", Data: map[string]interface{}{
					"importedPath": filepath.Join(downloadDir, "Show.S02.1080p.WEB-DL", "Show.S02E03.mkv"),
				}},
			},
			want: true,
		},
		{
			name:        "non-import event is ignored",
			releaseRoot: filepath.Join(downloadDir, "Show.S01E01.mkv"),
			records: []HistoryRecord{
				{EventType: "grabbed", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "Show.S01E01.mkv"),
				}},
			},
			want: false,
		},
		{
			name:        "unrelated sibling release is not matched",
			releaseRoot: filepath.Join(downloadDir, "Show.S01.1080p.WEB-DL"),
			records: []HistoryRecord{
				{EventType: "downloadFolderImported", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "Other.Show.S01.1080p.WEB-DL", "Other.S01E01.mkv"),
				}},
			},
			want: false,
		},
		{
			name:        "prefix sibling not treated as nested",
			releaseRoot: filepath.Join(downloadDir, "Show.S01"),
			records: []HistoryRecord{
				{EventType: "downloadFolderImported", Data: map[string]interface{}{
					"droppedPath": filepath.Join(downloadDir, "Show.S01.Extended", "ep.mkv"),
				}},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newTestServer(t, HistoryResponse{Records: tc.records, TotalRecords: len(tc.records)})
			defer srv.Close()

			c := NewClient("sonarr", "test-key", srv.URL)

			got, err := c.CheckImported(context.Background(), tc.releaseRoot)
			if err != nil {
				t.Fatalf("CheckImported returned error: %v", err)
			}

			if got != tc.want {
				t.Fatalf("CheckImported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		apiKey  string
		baseURL string
		want    bool
	}{
		{name: "fully configured", apiKey: "k", baseURL: "http://x", want: true},
		{name: "missing key", apiKey: "", baseURL: "http://x", want: false},
		{name: "missing url", apiKey: "k", baseURL: "", want: false},
		{name: "empty", apiKey: "", baseURL: "", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := NewClient("radarr", tc.apiKey, tc.baseURL)
			if got := c.IsConfigured(); got != tc.want {
				t.Fatalf("IsConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}
