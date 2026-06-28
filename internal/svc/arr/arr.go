package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// importEventTypes are the *arr history event types that indicate a release was
// successfully imported into the library. Sonarr and Radarr both emit
// "downloadFolderImported" for folder/multi-file releases; single-file imports may
// surface as "downloadImported" depending on the app/version.
var importEventTypes = map[string]struct{}{
	"downloadFolderImported": {},
	"downloadImported":       {},
}

// importPathKeys are the history record data fields that may carry the local
// download path that was imported. "droppedPath" is the path inside DOWNLOAD_DIR;
// the others are checked defensively across Sonarr/Radarr versions.
var importPathKeys = []string{"droppedPath", "importedPath", "path", "outputPath"}

// Client represents an *arr API client.
type Client struct {
	client  *http.Client
	name    string
	apiKey  string
	baseURL string
}

// NewClient creates a new *arr API client. name is a human-readable identifier
// (e.g. "sonarr" or "radarr") used for structured logging.
func NewClient(name, apiKey, baseURL string) *Client {
	return &Client{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
	}
}

// Name returns the human-readable identifier of the *arr app.
func (c *Client) Name() string {
	return c.name
}

// IsConfigured reports whether the client has the credentials required to talk to
// the *arr API. Unconfigured clients should be skipped rather than queried.
func (c *Client) IsConfigured() bool {
	return c.apiKey != "" && c.baseURL != ""
}

type HistoryRecord struct {
	EventType string                 `json:"eventType"`
	Data      map[string]interface{} `json:"data"`
}

type HistoryResponse struct {
	Records      []HistoryRecord `json:"records"`
	TotalRecords int             `json:"totalRecords"`
}

// CheckImported reports whether the release rooted at releaseRoot (an absolute path
// under DOWNLOAD_DIR) has been imported by the *arr application. It walks the import
// history and matches any import event whose recorded local path equals releaseRoot
// or is nested under it (covering both single-file and folder/multi-file releases).
func (c *Client) CheckImported(ctx context.Context, releaseRoot string) (bool, error) {
	inspected := 0
	page := 0

	for {
		url := fmt.Sprintf("%s/api/v3/history?includeSeries=false&includeEpisode=false&page=%d&pageSize=1000", c.baseURL, page)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("X-Api-Key", c.apiKey)

		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

		resp, err := c.client.Do(req)
		if err != nil {
			return false, fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("url: %s, status: %d", url, resp.StatusCode)
		}

		var historyResponse HistoryResponse
		if err := json.NewDecoder(resp.Body).Decode(&historyResponse); err != nil {
			return false, fmt.Errorf("failed to decode response: %w", err)
		}

		for _, record := range historyResponse.Records {
			if recordMatchesRelease(record, releaseRoot) {
				return true, nil
			}

			inspected++
		}

		if historyResponse.TotalRecords > inspected {
			page++
		} else {
			return false, nil
		}
	}
}

// recordMatchesRelease returns true if the history record is an import event whose
// recorded local path belongs to the given release root.
func recordMatchesRelease(record HistoryRecord, releaseRoot string) bool {
	if _, ok := importEventTypes[record.EventType]; !ok {
		return false
	}

	for _, key := range importPathKeys {
		path, ok := record.Data[key].(string)
		if !ok || path == "" {
			continue
		}

		if pathWithinRelease(path, releaseRoot) {
			return true
		}
	}

	return false
}

// pathWithinRelease reports whether candidate is the release root itself or a path
// nested under it. Both inputs are cleaned so trailing-separator differences between
// putioarr and *arr do not cause false negatives.
func pathWithinRelease(candidate, releaseRoot string) bool {
	candidate = filepath.Clean(candidate)
	releaseRoot = filepath.Clean(releaseRoot)

	if candidate == releaseRoot {
		return true
	}

	return strings.HasPrefix(candidate, releaseRoot+string(os.PathSeparator))
}
