package putio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/transfer"
	putio "github.com/putdotio/go-putio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTorrentFilename_ValidExtensions(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{
			name:     "lowercase .torrent",
			filename: "test.torrent",
			wantErr:  false,
		},
		{
			name:     "uppercase .TORRENT",
			filename: "test.TORRENT",
			wantErr:  false,
		},
		{
			name:     "mixed case .Torrent",
			filename: "test.Torrent",
			wantErr:  false,
		},
		{
			name:     "invalid .txt extension",
			filename: "test.txt",
			wantErr:  true,
		},
		{
			name:     "invalid .tor extension",
			filename: "test.tor",
			wantErr:  true,
		},
		{
			name:     "no extension",
			filename: "test",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTorrentFilename(tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTorrentFilename() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				var invalidErr *transfer.InvalidContentError
				if !errors.As(err, &invalidErr) {
					t.Errorf("expected InvalidContentError, got %T", err)
				}
				if invalidErr.Filename != tt.filename {
					t.Errorf("expected filename %q, got %q", tt.filename, invalidErr.Filename)
				}
			}
		})
	}
}

func TestAddTransferByBytes_InvalidExtension(t *testing.T) {
	client := NewClient("test-token")

	torrentBytes := []byte("fake content")
	_, err := client.AddTransferByBytes(context.Background(), torrentBytes, "test.txt", "")

	if err == nil {
		t.Fatal("expected error for invalid extension, got nil")
	}

	var invalidErr *transfer.InvalidContentError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected InvalidContentError, got %T: %v", err, err)
	}

	if invalidErr.Filename != "test.txt" {
		t.Errorf("expected filename 'test.txt', got %q", invalidErr.Filename)
	}

	if invalidErr.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestAddTransferByBytes_FileTooLarge(t *testing.T) {
	client := NewClient("test-token")

	// Create 11MB of data (exceeds 10MB limit)
	torrentBytes := make([]byte, 11*1024*1024)
	_, err := client.AddTransferByBytes(context.Background(), torrentBytes, "large.torrent", "")

	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}

	var invalidErr *transfer.InvalidContentError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected InvalidContentError, got %T: %v", err, err)
	}

	if invalidErr.Filename != "large.torrent" {
		t.Errorf("expected filename 'large.torrent', got %q", invalidErr.Filename)
	}

	// Verify the error message mentions size
	expectedSubstring := "exceeds maximum"
	if invalidErr.Reason == "" {
		t.Error("expected non-empty reason")
	} else {
		// Check if reason contains expected substring
		found := false
		for i := 0; i+len(expectedSubstring) <= len(invalidErr.Reason); i++ {
			if invalidErr.Reason[i:i+len(expectedSubstring)] == expectedSubstring {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected reason to contain %q, got %q", expectedSubstring, invalidErr.Reason)
		}
	}
}

func newTestClient(serverURL string) *Client {
	goputioClient := putio.NewClient(nil)
	u, _ := url.Parse(serverURL)
	goputioClient.BaseURL = u

	return &Client{putioClient: goputioClient}
}

// TestGetFilesRecursively_SingleFileRootedAtBasePath verifies that a single-file transfer is
// placed inside a folder named after basePath (the transfer name) verbatim, regardless of the
// Put.io file name or its extension. This is the core of the issue #5 fix: the on-disk root must
// equal the name advertised to Sonarr/Radarr so the import path always exists.
func TestGetFilesRecursively_SingleFileRootedAtBasePath(t *testing.T) {
	tests := []struct {
		name         string
		basePath     string
		fileName     string
		fileType     string
		expectedPath string
	}{
		{
			name:         "transfer name differs from file name",
			basePath:     "The.Movie.2024.1080p.BluRay.x265-GROUP",
			fileName:     "the.movie.short.mkv",
			fileType:     "VIDEO",
			expectedPath: "The.Movie.2024.1080p.BluRay.x265-GROUP/the.movie.short.mkv",
		},
		{
			name:         "release name with dots is not truncated",
			basePath:     "Dragon Ball Z (1993) (1080p BluRay x265 HEVC 10bit MLPFBA 5.1 SAMPA)",
			fileName:     "Dragon Ball Z (1993) (1080p BluRay x265 SAMPA).mkv",
			fileType:     "VIDEO",
			expectedPath: "Dragon Ball Z (1993) (1080p BluRay x265 HEVC 10bit MLPFBA 5.1 SAMPA)/Dragon Ball Z (1993) (1080p BluRay x265 SAMPA).mkv",
		},
		{
			name:         "transfer name equals file name without extension",
			basePath:     "the_movie",
			fileName:     "the_movie.mkv",
			fileType:     "VIDEO",
			expectedPath: "the_movie/the_movie.mkv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/files/100", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"file":{"id":100,"name":%q,"size":1000,"file_type":%q,"content_type":"video/x-matroska"}}`,
					tt.fileName, tt.fileType)
			})
			server := httptest.NewServer(mux)
			defer server.Close()

			client := newTestClient(server.URL)
			files, err := client.getFilesRecursively(context.Background(), 100, tt.basePath)

			require.NoError(t, err)
			require.Len(t, files, 1)
			assert.Equal(t, tt.expectedPath, files[0].Path)
		})
	}
}

// TestGetTaggedTorrents_FilesRootedAtTransferName reproduces issue #5: Put.io delivers a transfer
// under a torrent-internal file name that differs from the release/transfer name. The downloaded
// files must be rooted under the transfer name (what we advertise to Sonarr/Radarr), not the
// Put.io file name, otherwise *arr looks for a path that never exists and import never runs.
func TestGetTaggedTorrents_FilesRootedAtTransferName(t *testing.T) {
	const (
		transferName = "Dragon Ball Z (1993) (1080p BluRay x265 HEVC 10bit MLPFBA 5.1 SAMPA)"
		putioFile    = "Dragon Ball Z (1993) (1080p BluRay x265 SAMPA).mkv"
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/transfers/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"transfers":[{
			"id":1,"name":%q,"file_id":100,"save_parent_id":200,
			"status":"COMPLETED","percent_done":100,"size":1000,
			"source":"magnet:test","downloaded":1000,
			"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
		}]}`, transferName)
	})
	mux.HandleFunc("/v2/files/", func(w http.ResponseWriter, r *http.Request) {
		fileID := strings.TrimPrefix(r.URL.Path, "/v2/files/")
		w.Header().Set("Content-Type", "application/json")

		switch fileID {
		case "200":
			fmt.Fprint(w, `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`)
		case "100":
			fmt.Fprintf(w, `{"file":{"id":100,"name":%q,"size":1000,"file_type":"VIDEO","content_type":"video/x-matroska"}}`, putioFile)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error_type":"NOT_FOUND","error_message":"not found"}`)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(server.URL)
	torrents, err := client.GetTaggedTorrents(context.Background(), "mytag")

	require.NoError(t, err)
	require.Len(t, torrents, 1)
	assert.Equal(t, transferName, torrents[0].Name)
	require.Len(t, torrents[0].Files, 1)
	// The file must live under the transfer name, not the (shorter) Put.io file name.
	assert.Equal(t, filepath.Join(transferName, putioFile), torrents[0].Files[0].Path)
}

func TestGetTaggedTorrents_SaveParentIDMatching(t *testing.T) {
	tests := []struct {
		name         string
		tag          string
		transfers    string
		fileHandlers map[string]string // file ID -> response body
		fileStatus   map[string]int    // file ID -> status code
		wantCount    int
		wantNames    []string
		wantLabels   []string
	}{
		{
			name: "matching_tag",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":1,"name":"test-transfer","file_id":100,"save_parent_id":200,
				"status":"COMPLETED","percent_done":100,"size":1000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
			}]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
				"100": `{"file":{"id":100,"name":"test-file.mkv","size":1000,"file_type":"VIDEO","content_type":"video/x-matroska"}}`,
			},
			wantCount:  1,
			wantNames:  []string{"test-transfer"},
			wantLabels: []string{"mytag"},
		},
		{
			name: "non_matching_tag",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":2,"name":"other-transfer","file_id":100,"save_parent_id":200,
				"status":"COMPLETED","percent_done":100,"size":1000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
			}]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"othertag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
			},
			wantCount: 0,
		},
		{
			name: "saveparentid_zero",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":3,"name":"no-parent-transfer","file_id":100,"save_parent_id":0,
				"status":"COMPLETED","percent_done":100,"size":1000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
			}]}`,
			wantCount: 0,
		},
		{
			name: "in_progress_fileid_zero_returned",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":4,"name":"in-progress-transfer","file_id":0,"save_parent_id":200,
				"status":"DOWNLOADING","percent_done":50,"size":2000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":5,"peers_getting_from_us":0,"peers_sending_to_us":3,
				"down_speed":1048576
			}]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
			},
			wantCount: 1,
		},
		{
			name: "parent_fetch_error",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":5,"name":"error-transfer","file_id":100,"save_parent_id":999,
				"status":"COMPLETED","percent_done":100,"size":1000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
			}]}`,
			fileHandlers: map[string]string{},
			fileStatus: map[string]int{
				"999": http.StatusInternalServerError,
			},
			wantCount: 0,
		},
		{
			name: "multiple_transfers_mixed",
			tag:  "mytag",
			transfers: `{"transfers":[
				{"id":10,"name":"matching-transfer","file_id":100,"save_parent_id":200,
				 "status":"COMPLETED","percent_done":100,"size":1000,
				 "source":"magnet:test1","downloaded":1000,
				 "peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0},
				{"id":11,"name":"wrong-tag-transfer","file_id":101,"save_parent_id":201,
				 "status":"COMPLETED","percent_done":100,"size":2000,
				 "source":"magnet:test2","downloaded":2000,
				 "peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0},
				{"id":12,"name":"no-parent-transfer","file_id":102,"save_parent_id":0,
				 "status":"COMPLETED","percent_done":100,"size":3000,
				 "source":"magnet:test3","downloaded":3000,
				 "peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0}
			]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
				"100": `{"file":{"id":100,"name":"matching-file.mkv","size":1000,"file_type":"VIDEO","content_type":"video/x-matroska"}}`,
				"201": `{"file":{"id":201,"name":"othertag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
			},
			wantCount:  1,
			wantNames:  []string{"matching-transfer"},
			wantLabels: []string{"mytag"},
		},
		{
			name: "in_progress_transfer_returned",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":20,"name":"active-download","file_id":0,"save_parent_id":200,
				"status":"DOWNLOADING","percent_done":50,"size":2000,
				"source":"magnet:test","downloaded":1000,
				"peers_connected":5,"peers_getting_from_us":0,"peers_sending_to_us":3,
				"down_speed":1048576
			}]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
			},
			wantCount: 1,
		},
		{
			name: "completed_transfer_has_files",
			tag:  "mytag",
			transfers: `{"transfers":[{
				"id":21,"name":"completed-download","file_id":100,"save_parent_id":200,
				"status":"COMPLETED","percent_done":100,"size":1500,
				"source":"magnet:test","downloaded":1500,
				"peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0
			}]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
				"100": `{"file":{"id":100,"name":"completed-file.mkv","size":1500,"file_type":"VIDEO","content_type":"video/x-matroska"}}`,
			},
			wantCount: 1,
		},
		{
			name: "mixed_inprogress_and_completed",
			tag:  "mytag",
			transfers: `{"transfers":[
				{"id":30,"name":"in-progress","file_id":0,"save_parent_id":200,
				 "status":"DOWNLOADING","percent_done":50,"size":2000,
				 "source":"magnet:test1","downloaded":1000,
				 "peers_connected":5,"peers_getting_from_us":0,"peers_sending_to_us":3,
				 "down_speed":524288},
				{"id":31,"name":"completed","file_id":100,"save_parent_id":200,
				 "status":"COMPLETED","percent_done":100,"size":1500,
				 "source":"magnet:test2","downloaded":1500,
				 "peers_connected":0,"peers_getting_from_us":0,"peers_sending_to_us":0}
			]}`,
			fileHandlers: map[string]string{
				"200": `{"file":{"id":200,"name":"mytag","size":0,"file_type":"FOLDER","content_type":"application/x-directory"}}`,
				"100": `{"file":{"id":100,"name":"completed-file.mkv","size":1500,"file_type":"VIDEO","content_type":"video/x-matroska"}}`,
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()

			mux.HandleFunc("/v2/transfers/list", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.transfers)
			})

			mux.HandleFunc("/v2/files/", func(w http.ResponseWriter, r *http.Request) {
				fileID := strings.TrimPrefix(r.URL.Path, "/v2/files/")
				w.Header().Set("Content-Type", "application/json")

				if tt.fileStatus != nil {
					if status, ok := tt.fileStatus[fileID]; ok {
						w.WriteHeader(status)
						fmt.Fprint(w, `{"error_type":"ERROR","error_message":"server error"}`)
						return
					}
				}

				if tt.fileHandlers != nil {
					if body, ok := tt.fileHandlers[fileID]; ok {
						fmt.Fprint(w, body)
						return
					}
				}

				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error_type":"NOT_FOUND","error_message":"not found"}`)
			})

			server := httptest.NewServer(mux)
			defer server.Close()

			client := newTestClient(server.URL)
			torrents, err := client.GetTaggedTorrents(context.Background(), tt.tag)

			require.NoError(t, err)
			assert.Len(t, torrents, tt.wantCount)

			for i, name := range tt.wantNames {
				if i < len(torrents) {
					assert.Equal(t, name, torrents[i].Name)
				}
			}

			for i, label := range tt.wantLabels {
				if i < len(torrents) {
					assert.Equal(t, label, torrents[i].Label)
				}
			}

			// Additional verification for specific test cases
			switch tt.name {
			case "in_progress_fileid_zero_returned", "in_progress_transfer_returned":
				if len(torrents) > 0 {
					assert.Empty(t, torrents[0].Files, "in-progress transfer should have empty Files array")
					assert.Equal(t, int64(5), torrents[0].PeersConnected, "PeersConnected should be populated")
					assert.Equal(t, int64(3), torrents[0].PeersSendingToUs, "PeersSendingToUs should be populated")
					assert.Equal(t, int64(1048576), torrents[0].DownloadSpeed, "DownloadSpeed should be populated")
				}
			case "completed_transfer_has_files":
				if len(torrents) > 0 {
					assert.NotEmpty(t, torrents[0].Files, "completed transfer should have populated Files array")
				}
			case "mixed_inprogress_and_completed":
				if len(torrents) >= 2 {
					assert.Empty(t, torrents[0].Files, "first transfer (in-progress) should have empty Files array")
					assert.NotEmpty(t, torrents[1].Files, "second transfer (completed) should have populated Files array")
					assert.Equal(t, int64(524288), torrents[0].DownloadSpeed, "in-progress transfer should have DownloadSpeed")
				}
			}
		})
	}
}
