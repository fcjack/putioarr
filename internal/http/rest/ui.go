package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
	"github.com/italolelis/seedbox_downloader/internal/version"
)

// ConfigSnapshot is the non-secret configuration exposed to the Web UI.
type ConfigSnapshot struct {
	Version           string  `json:"version"`
	DownloadDir       string  `json:"downloadDir"`
	TargetLabel       string  `json:"targetLabel"`
	MaxParallel       int     `json:"maxParallel"`
	PollingInterval   string  `json:"pollingInterval"`
	CleanupInterval   string  `json:"cleanupInterval"`
	KeepDownloadedFor string  `json:"keepDownloadedFor"`
	DownloadClient    string  `json:"downloadClient"`
	PutioSeedRatio    float64 `json:"putioSeedRatio"`
	SonarrConfigured  bool    `json:"sonarrConfigured"`
	RadarrConfigured  bool    `json:"radarrConfigured"`
}

// UIHandler exposes the JSON REST API consumed by the putioarr Web UI. It is mounted
// on a dedicated port, separate from the Transmission RPC used by Sonarr/Radarr.
type UIHandler struct {
	svc    *transfers.Service
	config ConfigSnapshot
}

// NewUIHandler creates a new Web UI API handler.
func NewUIHandler(svc *transfers.Service, config ConfigSnapshot) *UIHandler {
	return &UIHandler{svc: svc, config: config}
}

// Routes returns the API router for the Web UI.
func (h *UIHandler) Routes() http.Handler {
	r := chi.NewRouter()

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", h.handleHealth)
		r.Get("/config", h.handleConfig)

		r.Get("/transfers", h.handleListTransfers)
		r.Get("/transfers/{id}", h.handleGetTransfer)
		r.Post("/transfers/{id}/retry", h.handleRetryTransfer)
		r.Post("/transfers/{id}/cancel", h.handleCancelTransfer)
		r.Delete("/transfers/{id}", h.handleDeleteTransfer)

		r.Post("/admin/db/reset", h.handleResetDB)
		r.Post("/admin/downloads/purge", h.handlePurgeDownloads)
	})

	return r
}

func (h *UIHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version.String(),
	})
}

func (h *UIHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, h.config)
}

func (h *UIHandler) handleResetDB(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ResetDB(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, err)

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]string{"status": "database reset"})
}

func (h *UIHandler) handlePurgeDownloads(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.PurgeDownloads(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, err)

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]string{"status": "download directory purged"})
}

func (h *UIHandler) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filters := transfers.ListFilters{
		Name:   r.URL.Query().Get("name"),
		Status: r.URL.Query().Get("status"),
		Label:  r.URL.Query().Get("label"),
	}

	views, err := h.svc.List(ctx, filters)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, err)

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{"transfers": views})
}

func (h *UIHandler) handleGetTransfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	detail, err := h.svc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, transfers.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, err)

			return
		}

		writeError(w, r, http.StatusBadGateway, err)

		return
	}

	writeJSON(w, r, http.StatusOK, detail)
}

func (h *UIHandler) handleRetryTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.svc.Retry(r.Context(), id); err != nil {
		writeError(w, r, http.StatusBadGateway, err)

		return
	}

	writeJSON(w, r, http.StatusAccepted, map[string]string{"status": "requeued", "id": id})
}

func (h *UIHandler) handleCancelTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.svc.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, transfers.ErrNoActiveDownload) {
			writeError(w, r, http.StatusConflict, err)

			return
		}

		writeError(w, r, http.StatusBadGateway, err)

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]string{"status": "cancelled", "id": id})
}

func (h *UIHandler) handleDeleteTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q := r.URL.Query()
	all := q.Has("all")

	scopes := transfers.DeleteScopes{
		Putio: all || q.Has("putio"),
		Local: all || q.Has("local"),
		DB:    all || q.Has("db"),
	}

	if !scopes.Any() {
		writeError(w, r, http.StatusBadRequest, errors.New("at least one delete scope required: putio, local, db, or all"))

		return
	}

	if err := h.svc.Delete(r.Context(), id, scopes); err != nil {
		writeError(w, r, http.StatusBadGateway, err)

		return
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status": "deleted",
		"id":     id,
		"scopes": map[string]bool{"putio": scopes.Putio, "local": scopes.Local, "db": scopes.DB},
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logctx.LoggerFromContext(r.Context()).ErrorContext(r.Context(), "failed to encode JSON response", "err", err)
	}
}

// writeError writes a JSON error response and logs server-side failures.
func writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	ctx := r.Context()

	if status >= http.StatusInternalServerError {
		logctx.LoggerFromContext(ctx).ErrorContext(ctx, "request failed", "status", status, "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if encodeErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encodeErr != nil {
		logctx.LoggerFromContext(ctx).ErrorContext(ctx, "failed to encode error response", "err", encodeErr)
	}
}
