package daemon

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func localReviewReadOnly(command protocol.LocalReviewCommand) bool {
	if command.Action == "environment" || command.Environment != nil {
		if command.Action != "environment" || command.Environment == nil {
			return false
		}
		switch command.Environment.Action {
		case "inventory", "cache_preview", "cleanup_preview", "archive_preview", "archives":
			return true
		default:
			return false
		}
	}
	return isReadReviewAction(command.Action)
}

func (d *Daemon) runClaimedReview(ctx context.Context, command protocol.LocalReviewCommand) (protocol.LocalReviewResult, bool) {
	if !localReviewReadOnly(command) || !command.CancellationSupported {
		return d.runRemoteReview(ctx, command), true
	}
	reading, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		d.watchReviewReader(reading, command, cancel)
	}()
	result := d.runRemoteReview(reading, command)
	report := reading.Err() == nil
	cancel()
	<-stopped
	return result, report
}

// Probe only transient claim liveness. Network errors or malformed replies do
// not imply cancellation; the bounded read deadline still stops orphaned work.
func (d *Daemon) watchReviewReader(ctx context.Context, command protocol.LocalReviewCommand, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	path := "/api/daemon/runtimes/" + command.RuntimeID + "/local-reviews/relay/" + command.ID + "/status"
	for {
		probe, stop := context.WithTimeout(ctx, 2*time.Second)
		var status protocol.LocalReviewStatus
		err := d.client.postJSON(probe, path, protocol.LocalReviewStatusRequest{ClaimToken: command.ClaimToken}, &status)
		stop()
		if err == nil && status.Active != nil && !*status.Active {
			cancel()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
