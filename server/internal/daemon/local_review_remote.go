package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type localReviewCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *localReviewCapture) Header() http.Header    { return w.header }
func (w *localReviewCapture) WriteHeader(status int) { w.status = status }
func (w *localReviewCapture) Write(data []byte) (int, error) {
	if w.body.Len()+len(data) > 12<<20 {
		return 0, fmt.Errorf("review response too large")
	}
	return w.body.Write(data)
}

// runRemoteReview reuses the same task ownership and filesystem locks as local IPC.
// Decisions arrive through authenticated forwarding, but approval state is
// checked and persisted by the owning runtime rather than inferred from merge.
func (d *Daemon) runRemoteReview(ctx context.Context, command protocol.LocalReviewCommand) protocol.LocalReviewResult {
	result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken}
	if d.findRuntime(command.RuntimeID) == nil {
		result.Error = "runtime is no longer registered here"
		return result
	}
	input := worktreeReviewRequest{TaskID: command.TaskID, WorkspaceID: command.WorkspaceID, Path: command.Path, Target: command.Target, SnapshotID: command.SnapshotID, Action: "read"}
	input.CommandID = command.ID
	if command.CommandID != "" {
		input.CommandID = command.CommandID
	}
	input.ActorID, input.Comment = command.ActorID, command.Comment
	if command.Action == "merge" {
		if recovered, ok := d.recoverRemoteMerge(ctx, input, command.ClaimToken); ok {
			return recovered
		}
	}
	call := func(action string) (worktreeReviewResponse, error) {
		input.Action = action
		data, err := json.Marshal(input)
		if err != nil {
			return worktreeReviewResponse{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/worktrees/review", bytes.NewReader(data))
		if err != nil {
			return worktreeReviewResponse{}, err
		}
		req.Header.Set("Authorization", "Bearer "+d.client.Token())
		req.Header.Set("X-Multica-Profile", d.cfg.Profile)
		w := &localReviewCapture{header: http.Header{}, status: http.StatusOK}
		d.reviewOperationHandler(true)(w, req)
		if w.status != http.StatusOK {
			return worktreeReviewResponse{}, fmt.Errorf("%s", w.body.String())
		}
		var response worktreeReviewResponse
		err = json.Unmarshal(w.body.Bytes(), &response)
		return response, err
	}
	var response worktreeReviewResponse
	var err error
	switch command.Action {
	case "read", "submit", "approve", "request_changes":
		response, err = call(command.Action)
	case "merge":
		if command.SnapshotID == "" {
			result.Error = "approved snapshot required"
			return result
		}
		response, err = call("merge")
	default:
		result.Error = "unknown local review command"
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Snapshot, err = json.Marshal(response.Snapshot)
	if err != nil {
		result.Error = "could not encode review snapshot"
		return result
	}
	result.MergedCommit = response.Review.MergedCommit
	result.Review, err = json.Marshal(response.Review)
	if err != nil {
		result.Error = "could not encode runtime review record"
	}
	return result
}

// localReviewLoop claims transient forwarding requests. The runtime owns durable
// review records; the server retains responses only while a caller is waiting.
func (d *Daemon) localReviewLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, runtimeID := range d.allRuntimeIDs() {
			var claim protocol.LocalReviewClaim
			path := "/api/daemon/runtimes/" + runtimeID + "/local-reviews/relay/claim"
			if err := d.client.postJSON(ctx, path, struct{}{}, &claim); err != nil {
				continue
			}
			if claim.Command == nil {
				continue
			}
			command := *claim.Command
			if command.RuntimeID != runtimeID {
				d.logger.Warn("local review command runtime mismatch")
				continue
			}
			result := d.runRemoteReview(ctx, command)
			resultPath := "/api/daemon/runtimes/" + runtimeID + "/local-reviews/relay/" + command.ID + "/result"
			for attempt := 0; attempt < 5; attempt++ {
				if err := d.client.postJSON(ctx, resultPath, result, nil); err == nil {
					break
				}
				if attempt == 4 {
					d.logger.Warn("local review result unacknowledged", "command_id", command.ID)
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}
	}
}
