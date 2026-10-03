package scheduler

import (
	"context"
	"time"
)

func ProjectSupervisionJob(dispatcher IssueWakeupDispatcher) JobSpec {
	return JobSpec{Name: "project_supervision_dispatch", Cadence: 30 * time.Second, CatchUpMode: CatchUpLatestOnly, CatchUpWindow: time.Hour, RunTimeout: 45 * time.Second, StaleTimeout: time.Minute, HeartbeatInterval: 10 * time.Second, AllowStaleReentry: true, MaxAttempts: 1, Scopes: StaticScopes(ScopeGlobal), Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
		return HandlerResult{}, dispatcher.Tick(ctx)
	}}
}
