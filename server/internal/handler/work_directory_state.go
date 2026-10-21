package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) ListRuntimeWorkDirectories(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	paths, err := h.Queries.ListRuntimeWorkDirectories(r.Context(), db.ListRuntimeWorkDirectoriesParams{
		RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, AfterPath: r.URL.Query().Get("after"),
	})
	if err != nil {
		slog.Warn("list runtime work directories failed", "runtime_id", uuidToString(runtime.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "cannot list work directories")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.RuntimeWorkDirectoryPage{Paths: paths})
}

func (h *Handler) UpdateRuntimeWorkDirectories(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	var states []struct {
		Path    string `json:"path"`
		Missing *bool  `json:"missing"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&states) != nil || len(states) == 0 || len(states) > 500 || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "expected 1-500 directory states")
		return
	}
	seen := make(map[string]bool, len(states))
	for _, state := range states {
		if state.Missing == nil || strings.TrimSpace(state.Path) == "" || len(state.Path) > 4096 || seen[state.Path] {
			writeError(w, http.StatusBadRequest, "invalid or duplicate directory path")
			return
		}
		seen[state.Path] = true
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Warn("begin work directory update failed", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot update work directories")
		return
	}
	defer tx.Rollback(r.Context())
	queries := h.Queries.WithTx(tx)
	var changed int64
	for _, state := range states {
		var count int64
		count, err = queries.UpdateRuntimeWorkDirectoryState(r.Context(), db.UpdateRuntimeWorkDirectoryStateParams{
			RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, Path: state.Path, Missing: *state.Missing,
		})
		if err != nil {
			break
		}
		changed += count
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		slog.Warn("update runtime work directories failed", "runtime_id", uuidToString(runtime.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "cannot update work directories")
		return
	}
	if changed > 0 {
		h.Bus.Publish(events.Event{Type: protocol.EventRuntimeWorkDirectoriesUpdated, WorkspaceID: uuidToString(runtime.WorkspaceID), ActorType: "system",
			Payload: map[string]string{"workspace_id": uuidToString(runtime.WorkspaceID), "runtime_id": uuidToString(runtime.ID)},
		})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
