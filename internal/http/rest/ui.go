package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
)

// UIHandler exposes the JSON REST API consumed by the putioarr Web UI. It is mounted
// on a dedicated port, separate from the Transmission RPC used by Sonarr/Radarr.
type UIHandler struct {
	svc *transfers.Service
}

// NewUIHandler creates a new Web UI API handler.
func NewUIHandler(svc *transfers.Service) *UIHandler {
	return &UIHandler{svc: svc}
}

// Routes returns the API router for the Web UI.
func (h *UIHandler) Routes() http.Handler {
	r := chi.NewRouter()

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/transfers", h.handleListTransfers)
		r.Get("/transfers/{id}", h.handleGetTransfer)
	})

	return r
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
