package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func writeMirrorReason(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, struct {
		Reason string `json:"reason"`
	}{Reason: reason})
}

func writeMirrorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, daemonws.ErrMirrorCapacity):
		writeMirrorReason(w, http.StatusTooManyRequests, "mirror_capacity")
	case errors.Is(err, protocol.ErrInvalidMirrorContract):
		writeMirrorReason(w, http.StatusBadRequest, "invalid_mirror_contract")
	case errors.Is(err, context.DeadlineExceeded):
		writeMirrorReason(w, http.StatusGatewayTimeout, "daemon_timeout")
	default:
		writeMirrorReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
	}
}
