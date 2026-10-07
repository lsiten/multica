package main

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationRuntimeGCRetainsUnconfirmedServicesAndCleansStoppedInstances(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	fx := testutil.New(testPool, testWorkspaceID, testUserID)
	projectID := fx.Project(t, "GC application project")
	runtimeID := fx.Runtime(t, "GC application runtime", testutil.Cols{"status": "offline", "last_seen_at": time.Now().Add(-8 * 24 * time.Hour)})
	appID := fx.Insert(t, "application", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": projectID, "name": "GC service", "kind": "service", "created_by": testUserID})
	fx.Insert(t, "application_revision", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": appID, "revision": 1, "config": testutil.Raw(`'{"mode":"external","port":4100}'::jsonb`), "actor_type": "member", "actor_id": testUserID})
	instanceID := fx.Insert(t, "application_instance", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": appID, "runtime_id": runtimeID, "daemon_id": testUserID, "revision": 1, "generation": 2, "observed_generation": 1, "desired_state": "stopped", "process_state": "stopped"})
	queries := db.New(testPool)
	candidates, err := queries.ListStaleOfflineRuntimeGCCandidates(ctx, db.ListStaleOfflineRuntimeGCCandidatesParams{StaleSeconds: offlineRuntimeTTLSeconds, MaxPerTick: runtimeGCBatchSize})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate == parseUUID(runtimeID) {
			t.Fatal("unconfirmed application entered the retention GC batch")
		}
	}
	blocked, err := gcRuntime(ctx, testPool, queries, parseUUID(runtimeID))
	if err != nil || blocked.deleted {
		t.Fatalf("GC removed an unconfirmed service: %+v %v", blocked, err)
	}
	if _, err := queries.ReportApplicationInstance(ctx, db.ReportApplicationInstanceParams{ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID), ObservedGeneration: 2, ObservedRevision: 1, ProcessState: "stopped", HealthState: "unknown", Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	deleted, err := gcRuntime(ctx, testPool, queries, parseUUID(runtimeID))
	if err != nil || !deleted.deleted {
		t.Fatalf("confirmed stopped runtime was not collected: %+v %v", deleted, err)
	}
	var instances, definitions int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM application_instance WHERE id=$1", instanceID).Scan(&instances); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM application WHERE id=$1", appID).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if instances != 0 || definitions != 1 {
		t.Fatalf("GC should remove instances and retain definitions: instances=%d definitions=%d", instances, definitions)
	}
}
