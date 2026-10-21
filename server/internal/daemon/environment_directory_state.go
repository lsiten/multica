package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The physical owner reports filesystem facts through the control parent; it
// never receives server credentials or an unrestricted HTTP transport.
func environmentDirectoryStateFact(request *http.Request, runtimeID string) (environmentFact, error) {
	var states []protocol.WorkDirectoryState
	decoder := json.NewDecoder(io.LimitReader(request.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&states) != nil || decoder.Decode(new(any)) != io.EOF || len(states) == 0 || len(states) > 500 {
		return environmentFact{}, errors.New("invalid work directory state report")
	}
	return environmentFact{Kind: "work_directory_state", RuntimeID: runtimeID, DirectoryStates: states}, nil
}

func (c *environmentFactCallback) reportDirectoryStates(ctx context.Context, fact environmentFact) environmentFactReply {
	if fact.RuntimeID == "" || len(fact.DirectoryStates) == 0 || len(fact.DirectoryStates) > 500 {
		return environmentFactReply{Status: http.StatusBadRequest, Body: json.RawMessage(`{"error":"invalid directory report"}`)}
	}
	for _, state := range fact.DirectoryStates {
		if !filepath.IsAbs(state.Path) || len(state.Path) > 4096 {
			return environmentFactReply{Status: http.StatusBadRequest, Body: json.RawMessage(`{"error":"invalid directory path"}`)}
		}
	}
	endpoint := "/api/daemon/runtimes/" + url.PathEscape(fact.RuntimeID) + "/work-directories"
	if err := c.daemon.client.postJSON(ctx, endpoint, fact.DirectoryStates, nil); err != nil {
		return environmentFactReply{Status: http.StatusServiceUnavailable, Body: json.RawMessage(`{"error":"directory report unavailable"}`)}
	}
	return environmentFactReply{Status: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)}
}
