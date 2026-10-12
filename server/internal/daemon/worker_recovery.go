package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// recoverWorkerOutcomes adopts worker-owned durable results after the control
// parent was replaced. It never relaunches a provider: a ready worker is read,
// its exact execution identity is queued through the owner outbox, then the
// worker is stopped. Unknown or suspect records remain for a later audit.
func (d *Daemon) recoverWorkerOutcomes(ctx context.Context) error {
	parent, err := filepath.EvalSymlinks(filepath.Dir(d.cfg.NativeVscreenPreferencesPath))
	if err != nil {
		return err
	}
	root := filepath.Join(parent, "worker-service")
	locations, err := runtimeproc.DiscoverRecordLocations(root)
	if err != nil {
		return err
	}
	for _, location := range locations {
		record := location.Record
		expectedScope := runtimeproc.Scope{Backend: strings.TrimRight(d.cfg.ServerBaseURL, "/"), Account: d.accountID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "worker"}
		if record.Identity.Scope != expectedScope {
			continue
		}
		if record.State != "ready" && record.State != "draining" {
			continue
		}
		client, openErr := runtimeproc.NewClient(record)
		if openErr != nil {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, reqErr := client.Request("worker.result", record.Fence, nil)
		if reqErr != nil {
			cancel()
			continue
		}
		response, callErr := client.Call(readCtx, req)
		cancel()
		var status workerResultStatus
		if callErr == nil && response.Receipt != nil {
			if jsonErr := json.Unmarshal(response.Receipt.Result, &status); jsonErr != nil {
				callErr = jsonErr
			}
		}
		result := status.Result
		if result == nil && status.Artifact != nil {
			raw, artifactErr := runtimeproc.ReadReviewArtifact(runtimeproc.ReviewArtifactNamespace(location.Root), runtimeproc.ReviewArtifactName(status.Artifact.RequestID), status.Artifact.Hash, status.Artifact.Length)
			if artifactErr != nil {
				continue
			}
			var decoded workerRunResult
			if json.Unmarshal(raw, &decoded) != nil {
				continue
			}
			result = &decoded
		}
		if result == nil {
			pending, listErr := runtimeproc.ListReviewArtifactPending(location.Root)
			if listErr != nil {
				continue
			}
			for _, raw := range pending {
				var decoded workerRunResult
				if json.Unmarshal(raw, &decoded) == nil && decoded.Execution != nil {
					result = &decoded
					break
				}
			}
			if result == nil {
				continue
			}
		}
		identity := result.Execution
		if identity == nil {
			continue
		}
		kind := terminalTaskReportComplete
		if result.Status != "completed" {
			kind = terminalTaskReportFail
		}
		report := terminalTaskReport{kind: kind, taskID: identity.TaskID, output: result.Output, errorMessage: result.RunError, failureReason: taskRunFailureReason(errors.New(result.RunError)), execution: identity, accountID: d.accountID}
		if result.Status == "completed" {
			report.failureReason = ""
		}
		if len(result.Usage) > 0 {
			if err := d.queueTaskReport(terminalTaskReport{kind: taskUsageReport, taskID: identity.TaskID, usage: result.Usage, execution: identity, accountID: d.accountID}); err != nil {
				return err
			}
		}
		if err := d.queueTaskReport(report); err != nil {
			return err
		}
		stopReq, stopErr := client.Request("stop", record.Fence, nil)
		if stopErr == nil {
			stopReq.Deadline = time.Now().Add(5 * time.Second)
			_, _ = client.Call(ctx, stopReq)
		}
	}
	return nil
}
