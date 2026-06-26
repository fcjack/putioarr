package rest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
	"github.com/stretchr/testify/require"
)

// mockLocalDownloadTracker implements LocalDownloadTracker for testing.
type mockLocalDownloadTracker struct {
	records []storage.DownloadRecord
	err     error
}

func (m *mockLocalDownloadTracker) GetDownloads() ([]storage.DownloadRecord, error) {
	return m.records, m.err
}

// mockPutioClient implements DownloadClient interface for testing.
type mockPutioClient struct {
	addTransferFunc          func(ctx context.Context, magnetLink, parentName string) (*transfer.Transfer, error)
	addTransferByBytesFunc   func(ctx context.Context, content []byte, filename, parentName string) (*transfer.Transfer, error)
	getTaggedTorrentsFunc    func(ctx context.Context, label string) ([]*transfer.Transfer, error)
	addTransferCalled        bool
	addTransferByBytesCalled bool
	lastMagnetLink           string
	lastFilename             string
	lastParentName           string
}

func (m *mockPutioClient) AddTransfer(ctx context.Context, magnetLink, parentName string) (*transfer.Transfer, error) {
	m.addTransferCalled = true
	m.lastMagnetLink = magnetLink
	m.lastParentName = parentName
	if m.addTransferFunc != nil {
		return m.addTransferFunc(ctx, magnetLink, parentName)
	}
	return &transfer.Transfer{ID: "mock-transfer-id", Name: "mock-transfer"}, nil
}

func (m *mockPutioClient) AddTransferByBytes(ctx context.Context, content []byte, filename, parentName string) (*transfer.Transfer, error) {
	m.addTransferByBytesCalled = true
	m.lastFilename = filename
	m.lastParentName = parentName
	if m.addTransferByBytesFunc != nil {
		return m.addTransferByBytesFunc(ctx, content, filename, parentName)
	}
	return &transfer.Transfer{ID: "mock-transfer-id", Name: "mock-transfer"}, nil
}

func (m *mockPutioClient) GetTaggedTorrents(ctx context.Context, label string) ([]*transfer.Transfer, error) {
	if m.getTaggedTorrentsFunc != nil {
		return m.getTaggedTorrentsFunc(ctx, label)
	}
	return []*transfer.Transfer{}, nil
}

func (m *mockPutioClient) RemoveTransfers(ctx context.Context, ids []string, deleteData bool) error {
	return nil
}

func TestValidateBencodeStructure(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expectError bool
		errorReason string
	}{
		{
			name:        "valid torrent structure with info dict",
			data:        []byte("d4:infod4:name4:testee"), // {"info": {"name": "test"}}
			expectError: false,
		},
		{
			name:        "valid torrent with announce",
			data:        []byte("d8:announce3:url4:infod4:name4:testee"), // {"announce": "url", "info": {"name": "test"}}
			expectError: false,
		},
		{
			name:        "invalid bencode syntax - not bencode",
			data:        []byte("not bencode at all"),
			expectError: true,
			errorReason: "invalid bencode structure",
		},
		{
			name:        "invalid bencode syntax - truncated",
			data:        []byte("d4:info"), // Incomplete dictionary
			expectError: true,
			errorReason: "invalid bencode structure",
		},
		{
			name:        "root is list not dictionary",
			data:        []byte("l4:teste"), // ["test"]
			expectError: true,
			errorReason: "bencode root must be a dictionary",
		},
		{
			name:        "root is string not dictionary",
			data:        []byte("4:test"), // "test"
			expectError: true,
			errorReason: "bencode root must be a dictionary",
		},
		{
			name:        "root is integer not dictionary",
			data:        []byte("i42e"), // 42
			expectError: true,
			errorReason: "bencode root must be a dictionary",
		},
		{
			name:        "missing info field",
			data:        []byte("d4:name4:teste"), // {"name": "test"} - no "info"
			expectError: true,
			errorReason: "bencode missing required 'info' dictionary",
		},
		{
			name:        "empty dictionary",
			data:        []byte("de"), // {}
			expectError: true,
			errorReason: "bencode missing required 'info' dictionary",
		},
		{
			name:        "info is not dictionary",
			data:        []byte("d4:info4:teste"), // {"info": "test"} - info is string
			expectError: false,                    // Current implementation only checks for presence, not type
		},
		{
			name:        "empty data",
			data:        []byte{},
			expectError: true,
			errorReason: "invalid bencode structure",
		},
		{
			name:        "nil data treated as empty",
			data:        nil,
			expectError: true,
			errorReason: "invalid bencode structure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBencodeStructure(tt.data)

			if tt.expectError {
				require.Error(t, err, "expected error for case: %s", tt.name)

				// Verify error type is InvalidContentError
				var invalidErr *transfer.InvalidContentError
				require.True(t, errors.As(err, &invalidErr),
					"expected InvalidContentError, got %T", err)

				// Verify error reason contains expected substring
				require.Contains(t, invalidErr.Reason, tt.errorReason,
					"error reason should contain %q", tt.errorReason)
			} else {
				require.NoError(t, err, "unexpected error for case: %s", tt.name)
			}
		})
	}
}

func TestBase64DecodingEdgeCases(t *testing.T) {
	// Create test data with characters that will produce + or / in base64
	// This ensures URLEncoding will actually differ from StdEncoding
	testDataWithSpecialChars := []byte{0xff, 0xff, 0xff, 0xff} // Produces "////" in StdEncoding, "____" in URLEncoding

	tests := []struct {
		name        string
		input       string
		expectError bool
		description string
	}{
		{
			name:        "valid StdEncoding base64",
			input:       base64.StdEncoding.EncodeToString([]byte("d4:infod4:name4:testee")),
			expectError: false,
			description: "Standard encoding should decode successfully",
		},
		{
			name:        "wrong variant - URLEncoding with special chars",
			input:       base64.URLEncoding.EncodeToString(testDataWithSpecialChars),
			expectError: true,
			description: "URLEncoding uses -_ instead of +/ - StdEncoding decoder should reject _ characters",
		},
		{
			name:        "invalid characters",
			input:       "!!!invalid-base64!!!",
			expectError: true,
			description: "Non-base64 characters should fail",
		},
		{
			name:        "wrong padding",
			input:       "SGVsbG8gV29ybGQ", // Missing padding (should be SGVsbG8gV29ybGQ=)
			expectError: true,              // Go's StdEncoding is strict about padding
			description: "Missing padding should fail",
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true, // Empty decodes to empty bytes, but bencode validation fails
			description: "Empty input produces empty bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := base64.StdEncoding.DecodeString(tt.input)

			if tt.expectError {
				// Either base64 decode fails OR bencode validation fails
				if err != nil {
					return // Base64 decode failed - expected
				}
				// If decode succeeded, bencode validation should fail
				err = validateBencodeStructure(decoded)
				require.Error(t, err, "expected bencode validation to fail for: %s", tt.description)
			} else {
				require.NoError(t, err, "base64 decode should succeed for: %s", tt.description)
				// For valid cases, also check bencode validation passes
				if len(decoded) > 0 {
					err = validateBencodeStructure(decoded)
					require.NoError(t, err, "bencode validation should pass for: %s", tt.description)
				}
			}
		})
	}
}

func TestGenerateTorrentFilename(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantLen int
		wantExt string
	}{
		{
			name:    "generates hash-based filename",
			data:    []byte("d4:infod4:name4:testee"),
			wantLen: 24, // 16 chars hash + ".torrent" (8 chars)
			wantExt: ".torrent",
		},
		{
			name:    "different content produces different filename",
			data:    []byte("d4:infod4:name7:anothere"),
			wantLen: 24,
			wantExt: ".torrent",
		},
		{
			name:    "empty content still generates filename",
			data:    []byte{},
			wantLen: 24,
			wantExt: ".torrent",
		},
	}

	var previousFilename string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := generateTorrentFilename(tt.data)

			require.Len(t, filename, tt.wantLen,
				"filename should be %d characters", tt.wantLen)

			require.True(t, len(filename) > len(tt.wantExt),
				"filename should be longer than extension")

			ext := filename[len(filename)-len(tt.wantExt):]
			require.Equal(t, tt.wantExt, ext,
				"filename should end with %s", tt.wantExt)

			// Verify different content produces different filename
			if previousFilename != "" && tt.name == "different content produces different filename" {
				require.NotEqual(t, previousFilename, filename,
					"different content should produce different filename")
			}
			previousFilename = filename
		})
	}
}

func TestFormatTransmissionError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "InvalidContentError formatting",
			err: &transfer.InvalidContentError{
				Filename: "test.torrent",
				Reason:   "invalid base64 encoding",
			},
			expected: "invalid torrent: invalid base64 encoding",
		},
		{
			name: "NetworkError formatting",
			err: &transfer.NetworkError{
				Operation:  "upload",
				StatusCode: 503,
				APIMessage: "service unavailable",
			},
			expected: "upload failed: service unavailable",
		},
		{
			name: "DirectoryError formatting",
			err: &transfer.DirectoryError{
				DirectoryName: "itv",
				Reason:        "not found",
			},
			expected: "directory error: not found",
		},
		{
			name: "AuthenticationError formatting",
			err: &transfer.AuthenticationError{
				Operation: "upload",
			},
			expected: "authentication failed",
		},
		{
			name:     "generic error formatting",
			err:      errors.New("something went wrong"),
			expected: "error: something went wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTransmissionError(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestHandleTorrentAdd_MetaInfo_Success(t *testing.T) {
	// Create valid bencode content
	validTorrent := []byte("d4:infod4:name4:testee")
	metainfo := base64.StdEncoding.EncodeToString(validTorrent)

	mockClient := &mockPutioClient{
		addTransferByBytesFunc: func(ctx context.Context, content []byte, filename, parentName string) (*transfer.Transfer, error) {
			return &transfer.Transfer{
				ID:   "12345",
				Name: "test-transfer",
			}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := fmt.Sprintf(`{
		"method": "torrent-add",
		"arguments": {
			"metainfo": "%s"
		}
	}`, metainfo)

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "expected HTTP 200")

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "success", resp.Result)
	require.Contains(t, string(resp.Arguments), "torrent-added")

	// Verify correct method was called
	require.True(t, mockClient.addTransferByBytesCalled, "AddTransferByBytes should be called for metainfo")
	require.False(t, mockClient.addTransferCalled, "AddTransfer should not be called for metainfo")
}

func TestHandleTorrentAdd_MagnetLink_BackwardCompatibility(t *testing.T) {
	mockClient := &mockPutioClient{
		addTransferFunc: func(ctx context.Context, magnetLink, parentName string) (*transfer.Transfer, error) {
			return &transfer.Transfer{
				ID:   "67890",
				Name: "magnet-transfer",
			}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := `{
		"method": "torrent-add",
		"arguments": {
			"filename": "magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&dn=Test+Torrent"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "expected HTTP 200")

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "success", resp.Result)
	require.Contains(t, string(resp.Arguments), "torrent-added")

	// Verify correct method was called - this is the key backward compatibility check
	require.True(t, mockClient.addTransferCalled, "AddTransfer should be called for magnet links")
	require.False(t, mockClient.addTransferByBytesCalled, "AddTransferByBytes should not be called for magnet links")
	require.Contains(t, mockClient.lastMagnetLink, "magnet:", "magnet link should be passed to AddTransfer")
}

func TestHandleTorrentAdd_MetaInfo_PrioritizedOverFileName(t *testing.T) {
	// When both MetaInfo and FileName are present, MetaInfo should be used (API-06)
	validTorrent := []byte("d4:infod4:name4:testee")
	metainfo := base64.StdEncoding.EncodeToString(validTorrent)

	mockClient := &mockPutioClient{}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	// Request with BOTH metainfo and filename
	reqBody := fmt.Sprintf(`{
		"method": "torrent-add",
		"arguments": {
			"metainfo": "%s",
			"filename": "magnet:?xt=urn:btih:IGNORED"
		}
	}`, metainfo)

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// MetaInfo should be prioritized - AddTransferByBytes called, not AddTransfer
	require.True(t, mockClient.addTransferByBytesCalled, "MetaInfo should be prioritized over FileName")
	require.False(t, mockClient.addTransferCalled, "FileName should be ignored when MetaInfo present")
}

func TestHandleTorrentAdd_InvalidBase64_ReturnsTransmissionError(t *testing.T) {
	mockClient := &mockPutioClient{}
	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	// Invalid base64 - contains characters not in base64 alphabet
	reqBody := `{
		"method": "torrent-add",
		"arguments": {
			"metainfo": "!!!invalid-base64!!!"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	// Transmission protocol: HTTP 200 with error in result field
	require.Equal(t, http.StatusOK, w.Code, "Transmission errors should return HTTP 200")

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.Result, "invalid torrent", "error should indicate invalid torrent")
	require.Contains(t, resp.Result, "base64", "error should mention base64")
}

func TestHandleTorrentAdd_InvalidBencode_ReturnsTransmissionError(t *testing.T) {
	mockClient := &mockPutioClient{}
	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	// Valid base64 but invalid bencode content
	invalidBencode := base64.StdEncoding.EncodeToString([]byte("not valid bencode"))

	reqBody := fmt.Sprintf(`{
		"method": "torrent-add",
		"arguments": {
			"metainfo": "%s"
		}
	}`, invalidBencode)

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Transmission errors should return HTTP 200")

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.Result, "invalid torrent", "error should indicate invalid torrent")
	require.Contains(t, resp.Result, "bencode", "error should mention bencode")
}

func TestHandleTorrentAdd_AuthenticationRequired(t *testing.T) {
	mockClient := &mockPutioClient{}
	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := `{"method": "torrent-add", "arguments": {"filename": "magnet:?xt=urn:btih:TEST"}}`

	// Request without authentication
	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	// Note: NOT calling req.SetBasicAuth()

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code, "missing auth should return 401")
}

func TestHandleTorrentAdd_WrongCredentials(t *testing.T) {
	mockClient := &mockPutioClient{}
	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := `{"method": "torrent-add", "arguments": {"filename": "magnet:?xt=urn:btih:TEST"}}`

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("wronguser", "wrongpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code, "wrong credentials should return 401")
}

// TestHandleTorrentAdd_RealTorrentFile tests with a real .torrent file fixture (TEST-03 requirement).
// This test is skipped if the fixture file is not present - obtain a valid .torrent from your tracker.
func TestHandleTorrentAdd_RealTorrentFile(t *testing.T) {
	torrentPath := "testdata/valid.torrent"
	torrentData, err := os.ReadFile(torrentPath)
	if os.IsNotExist(err) {
		t.Skip("Skipping: testdata/valid.torrent not present. See testdata/README.md for instructions.")
	}
	require.NoError(t, err, "failed to read torrent file")

	metainfo := base64.StdEncoding.EncodeToString(torrentData)

	mockClient := &mockPutioClient{
		addTransferByBytesFunc: func(ctx context.Context, content []byte, filename, parentName string) (*transfer.Transfer, error) {
			// Verify the content matches what we sent
			require.Equal(t, torrentData, content, "torrent content should match fixture")
			return &transfer.Transfer{ID: "real-torrent-id", Name: "real-transfer"}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := fmt.Sprintf(`{
		"method": "torrent-add",
		"arguments": {
			"metainfo": "%s"
		}
	}`, metainfo)

	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "expected HTTP 200")

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "success", resp.Result, "should successfully process real torrent file")
	require.True(t, mockClient.addTransferByBytesCalled, "AddTransferByBytes should be called")
}

func TestHandleTorrentGet_StatusMapping(t *testing.T) {
	tests := []struct {
		name           string
		putioStatus    string
		expectedStatus TransmissionTorrentStatus
		expectedCode   int
	}{
		{"downloading", "DOWNLOADING", StatusDownload, 4},
		{"in_queue", "IN_QUEUE", StatusDownloadWait, 3},
		{"waiting", "WAITING", StatusDownloadWait, 3},
		{"finishing", "FINISHING", StatusCheck, 2},
		{"checking", "CHECKING", StatusCheck, 2},
		{"completed", "COMPLETED", StatusSeed, 6},
		{"finished", "FINISHED", StatusSeed, 6},
		{"seeding", "SEEDING", StatusSeed, 6},
		{"seedingwait", "SEEDINGWAIT", StatusSeedWait, 5},
		{"error", "ERROR", StatusStopped, 0},
		{"unknown", "UNKNOWN_NEW_STATUS", StatusStopped, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockPutioClient{
				getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
					return []*transfer.Transfer{
						{
							ID:     "1",
							Name:   "test",
							Size:   1000,
							Status: tt.putioStatus,
						},
					}, nil
				},
			}

			handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

			reqBody := `{"method": "torrent-get", "arguments": {"fields": ["status"]}}`
			req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
			req.SetBasicAuth("testuser", "testpass")

			w := httptest.NewRecorder()
			handler.Routes().ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var resp TransmissionResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, "success", resp.Result)

			var args struct {
				Torrents []TransmissionTorrent `json:"torrents"`
			}
			require.NoError(t, json.Unmarshal(resp.Arguments, &args))
			require.Len(t, args.Torrents, 1)
			require.Equal(t, tt.expectedStatus, args.Torrents[0].Status)
			require.Equal(t, tt.expectedCode, int(args.Torrents[0].Status))
		})
	}
}

func TestHandleTorrentGet_ErrorStringPopulated(t *testing.T) {
	tests := []struct {
		name               string
		status             string
		errorMessage       string
		expectErrorString  bool
		expectedErrorValue string
	}{
		{
			name:               "error status with message",
			status:             "ERROR",
			errorMessage:       "tracker unreachable",
			expectErrorString:  true,
			expectedErrorValue: "tracker unreachable",
		},
		{
			name:              "completed status with no error",
			status:            "COMPLETED",
			errorMessage:      "",
			expectErrorString: false,
		},
		{
			name:              "error status with empty message",
			status:            "ERROR",
			errorMessage:      "",
			expectErrorString: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockPutioClient{
				getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
					return []*transfer.Transfer{
						{
							ID:           "1",
							Name:         "test",
							Size:         1000,
							Status:       tt.status,
							ErrorMessage: tt.errorMessage,
						},
					}, nil
				},
			}

			handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

			reqBody := `{"method": "torrent-get", "arguments": {}}`
			req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
			req.SetBasicAuth("testuser", "testpass")

			w := httptest.NewRecorder()
			handler.Routes().ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var resp TransmissionResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

			var args struct {
				Torrents []TransmissionTorrent `json:"torrents"`
			}
			require.NoError(t, json.Unmarshal(resp.Arguments, &args))
			require.Len(t, args.Torrents, 1)

			if tt.expectErrorString {
				require.NotNil(t, args.Torrents[0].ErrorString)
				require.Equal(t, tt.expectedErrorValue, *args.Torrents[0].ErrorString)
			} else {
				require.Nil(t, args.Torrents[0].ErrorString)
			}
		})
	}
}

func TestHandleTorrentGet_PeerAndSpeedFields(t *testing.T) {
	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:                 "1",
					Name:               "test",
					Size:               1000,
					Status:             "DOWNLOADING",
					PeersConnected:     10,
					PeersSendingToUs:   5,
					PeersGettingFromUs: 3,
					DownloadSpeed:      5242880, // 5MB/s
				},
			}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "test-label", "/downloads", "", nil, nil)

	reqBody := `{"method": "torrent-get", "arguments": {}}`
	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var args struct {
		Torrents []TransmissionTorrent `json:"torrents"`
	}
	require.NoError(t, json.Unmarshal(resp.Arguments, &args))
	require.Len(t, args.Torrents, 1)

	torrent := args.Torrents[0]
	require.Equal(t, int64(10), torrent.PeersConnected)
	require.Equal(t, int64(5), torrent.PeersSendingToUs)
	require.Equal(t, int64(3), torrent.PeersGettingFromUs)
	require.Equal(t, int64(5242880), torrent.RateDownload)
}

func TestHandleTorrentGet_LabelsPopulated(t *testing.T) {
	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:     "1",
					Name:   "test",
					Size:   1000,
					Status: "DOWNLOADING",
				},
			}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "mytag", "/downloads", "", nil, nil)

	reqBody := `{"method": "torrent-get", "arguments": {}}`
	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var args struct {
		Torrents []TransmissionTorrent `json:"torrents"`
	}
	require.NoError(t, json.Unmarshal(resp.Arguments, &args))
	require.Len(t, args.Torrents, 1)

	require.Equal(t, []string{"mytag"}, args.Torrents[0].Labels)
}

// getTorrents is a helper that issues a torrent-get request and returns the parsed torrents.
func getTorrents(t *testing.T, handler *TransmissionHandler) []TransmissionTorrent {
	t.Helper()

	reqBody := `{"method": "torrent-get", "arguments": {}}`
	req := httptest.NewRequest(http.MethodPost, "/transmission/rpc", strings.NewReader(reqBody))
	req.SetBasicAuth("testuser", "testpass")

	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp TransmissionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "success", resp.Result)

	var args struct {
		Torrents []TransmissionTorrent `json:"torrents"`
	}
	require.NoError(t, json.Unmarshal(resp.Arguments, &args))

	return args.Torrents
}

// TestHandleTorrentGet_PutioCompleteLocalInProgress verifies the core invariant from issue #1:
// when Put.io reports COMPLETED but the local download is still in progress, the proxy must
// keep reporting status=downloading / isFinished=false so *arr does not import a partial file.
func TestHandleTorrentGet_PutioCompleteLocalInProgress(t *testing.T) {
	localDir := t.TempDir()

	// Simulate a partially written local file (1.5 GB of an expected 6.2 GB single-file mkv).
	const partialSize = int64(1_506_901_282)
	const totalSize = int64(6_662_229_608)
	relPath := "ExampleShow.S02E05.mkv"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, relPath), make([]byte, 1024), 0o644))
	require.NoError(t, os.Truncate(filepath.Join(localDir, relPath), partialSize))

	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       relPath,
					Size:       totalSize,
					Downloaded: totalSize, // Put.io reports its own transfer as fully downloaded
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: relPath, Size: totalSize}},
				},
			}, nil
		},
	}

	// No DB record yet (or status "downloading") -> local download not finished.
	tracker := &mockLocalDownloadTracker{records: []storage.DownloadRecord{{DownloadID: "1", Status: "downloading"}}}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", localDir, tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusDownload, got.Status, "must report downloading while local download in progress")
	require.False(t, got.IsFinished, "must not be finished until local file fully written")
	require.Equal(t, partialSize, got.DownloadedEver, "downloadedEver must reflect local progress")
	require.Equal(t, totalSize-partialSize, got.LeftUntilDone, "leftUntilDone must reflect local progress")
}

// TestHandleTorrentGet_PutioCompleteNoLocalRecord covers the race where Put.io is COMPLETED but
// the local download has not been claimed yet (no DB record). It must still report downloading.
func TestHandleTorrentGet_PutioCompleteNoLocalRecord(t *testing.T) {
	localDir := t.TempDir()

	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       "movie.mkv",
					Size:       1000,
					Downloaded: 1000,
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: "movie.mkv", Size: 1000}},
				},
			}, nil
		},
	}

	tracker := &mockLocalDownloadTracker{records: nil}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", localDir, tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusDownload, got.Status)
	require.False(t, got.IsFinished)
	require.Equal(t, int64(0), got.DownloadedEver, "no local file on disk yet")
	require.Equal(t, int64(1000), got.LeftUntilDone)
}

// TestHandleTorrentGet_LocalDownloadComplete verifies that once the local download is marked
// "downloaded" in the DB, the proxy reports seeding/finished so *arr can safely import.
func TestHandleTorrentGet_LocalDownloadComplete(t *testing.T) {
	localDir := t.TempDir()

	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       "movie.mkv",
					Size:       1000,
					Downloaded: 1000,
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: "movie.mkv", Size: 1000}},
				},
			}, nil
		},
	}

	tracker := &mockLocalDownloadTracker{records: []storage.DownloadRecord{{DownloadID: "1", Status: "downloaded"}}}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", localDir, tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusSeed, got.Status, "local download complete must report seeding")
	require.True(t, got.IsFinished, "local download complete must be finished")
	require.Equal(t, int64(0), got.LeftUntilDone)
	require.Equal(t, int64(1000), got.DownloadedEver)
}

// TestHandleTorrentGet_LocalDownloadFailed verifies failed local downloads surface an error/stopped state.
func TestHandleTorrentGet_LocalDownloadFailed(t *testing.T) {
	localDir := t.TempDir()

	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       "movie.mkv",
					Size:       1000,
					Downloaded: 1000,
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: "movie.mkv", Size: 1000}},
				},
			}, nil
		},
	}

	tracker := &mockLocalDownloadTracker{records: []storage.DownloadRecord{{DownloadID: "1", Status: "failed"}}}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", localDir, tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusStopped, got.Status)
	require.False(t, got.IsFinished)
	require.NotNil(t, got.ErrorString)
}

// TestHandleTorrentGet_DownloadingStatusUnaffected ensures in-progress Put.io transfers
// (still downloading on Put.io's side) report Put.io progress as before.
func TestHandleTorrentGet_DownloadingStatusUnaffected(t *testing.T) {
	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       "movie.mkv",
					Size:       1000,
					Downloaded: 400,
					Status:     "DOWNLOADING",
				},
			}, nil
		},
	}

	tracker := &mockLocalDownloadTracker{records: nil}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", t.TempDir(), tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusDownload, got.Status)
	require.False(t, got.IsFinished)
	require.Equal(t, int64(400), got.DownloadedEver, "in-flight Put.io transfers keep reporting Put.io progress")
	require.Equal(t, int64(600), got.LeftUntilDone)
}

// TestHandleTorrentGet_NilTrackerFallback ensures the handler still works without a tracker,
// falling back to Put.io status (preserving prior behavior for callers that pass nil).
func TestHandleTorrentGet_NilTrackerFallback(t *testing.T) {
	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       "movie.mkv",
					Size:       1000,
					Downloaded: 1000,
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: "movie.mkv", Size: 1000}},
				},
			}, nil
		},
	}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "tv", "/downloads", "", nil, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusSeed, got.Status, "without a tracker, fall back to Put.io status (legacy behavior)")
	require.True(t, got.IsFinished)
}

// TestHandleTorrentGet_DBLoadFailedStillChecksDisk verifies that a broken GetDownloads does not
// bypass the local-file gate: partial on-disk progress must still report as downloading.
func TestHandleTorrentGet_DBLoadFailedStillChecksDisk(t *testing.T) {
	localDir := t.TempDir()

	const partialSize = int64(600_000_000)
	const totalSize = int64(20_000_000_000)
	relPath := "Big.Movie.2024.mkv"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, relPath), make([]byte, 1024), 0o644))
	require.NoError(t, os.Truncate(filepath.Join(localDir, relPath), partialSize))

	mockClient := &mockPutioClient{
		getTaggedTorrentsFunc: func(ctx context.Context, label string) ([]*transfer.Transfer, error) {
			return []*transfer.Transfer{
				{
					ID:         "1",
					Name:       relPath,
					Size:       totalSize,
					Downloaded: totalSize,
					Status:     "COMPLETED",
					Files:      []*transfer.File{{ID: 1, Path: relPath, Size: totalSize}},
				},
			}, nil
		},
	}

	tracker := &mockLocalDownloadTracker{err: errors.New("scan mismatch")}

	handler := NewTransmissionHandler("testuser", "testpass", mockClient, "jac", "/jac", localDir, tracker, nil)

	torrents := getTorrents(t, handler)
	require.Len(t, torrents, 1)

	got := torrents[0]
	require.Equal(t, StatusDownload, got.Status)
	require.False(t, got.IsFinished)
	require.Equal(t, partialSize, got.DownloadedEver)
	require.Equal(t, totalSize-partialSize, got.LeftUntilDone)
}
