package daemon

import (
	"context"
	"errors"
)

func (d *Daemon) runWorkerExecution(ctx context.Context, worker *workerProcessClient, in workerRunInput) (result *workerRunResult, runErr error) {
	if worker == nil {
		return nil, errors.New("worker process is nil")
	}
	defer func() {
		worker.close()
		if worker.closeErr != nil {
			runErr = errors.Join(errWorkerOutcomeUncertain, runErr, worker.closeErr)
		}
	}()
	status, err := worker.bind(ctx, in)
	if err != nil {
		return nil, err
	}
	if !status.Launchable {
		return &workerRunResult{Status: "refused", RunError: status.ReasonCode}, nil
	}
	if !worker.ready() {
		return nil, errors.New("worker not ready after a launchable bind")
	}
	wakeup, unsubscribe := d.taskSupplementSignals.subscribe(in.Grant.Identity.TaskID)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.forwardSupplementWakeups(ctx, wakeup)
	}()
	defer func() { cancel(); <-done }()
	return worker.run(ctx)
}
