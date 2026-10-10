package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationServiceGrantFixture struct {
	fx                                                               *testutil.Fixture
	handler                                                          *Handler
	router                                                           http.Handler
	workspace, user, member, runtime, daemon, control, pat, instance string
	grant                                                            protocol.ApplicationServiceGrantResponse
}

func applicationServiceRequest(method, path, token string, body any) *http.Request {
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func applicationServiceTestRouter(h *Handler) http.Handler {
	router := chi.NewRouter()
	router.Route("/api/daemon", func(r chi.Router) {
		r.Use(middleware.DaemonAuth(h.Queries, nil, nil, nil))
		r.Get("/runtimes/{runtimeId}/application-service-grants", h.GetApplicationServiceGrantState)
		r.Post("/runtimes/{runtimeId}/application-service-grants", h.IssueApplicationServiceGrant)
		r.Post("/runtimes/{runtimeId}/application-service-grants/revoke", h.RevokeApplicationServiceGrant)
		r.Post("/runtimes/{runtimeId}/applications/claim", h.ClaimRuntimeApplications)
		r.Post("/runtimes/{runtimeId}/applications/sync", h.SyncRuntimeApplications)
		r.Post("/runtimes/{runtimeId}/applications/observe", h.ReportRuntimeApplication)
		r.Post("/runtimes/{runtimeId}/applications/steps/{stepId}/result", h.CompleteRuntimeApplication)
		r.Post("/runtimes/{runtimeId}/applications/steps/{stepId}/lease", h.RenewRuntimeApplicationLease)
		r.Get("/runtimes/{runtimeId}/applications/tunnel/control", h.ConnectApplicationControl)
		r.Get("/runtimes/{runtimeId}/applications/tunnel/data", h.ConnectApplicationStream)
		r.Post("/tasks/{taskId}/start", h.StartTask)
		r.Post("/runtimes/{runtimeId}/execution-supervisor", h.AcquireExecutionSupervisor)
	})
	router.With(middleware.Auth(h.Queries, nil, nil, nil)).Get("/api/account", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	router.Post("/api/application-connections/resolve", h.ResolveApplicationConnection)
	return router
}

func (f applicationServiceGrantFixture) path(suffix string) string {
	return "/api/daemon/runtimes/" + f.runtime + suffix
}
func (f applicationServiceGrantFixture) grantInput(generation int64) protocol.ApplicationServiceGrantRequest {
	return protocol.ApplicationServiceGrantRequest{ServiceInstanceID: f.instance, ExpectedGeneration: generation, Operations: []string{"sync", "claim", "observe", "result", "lease", "tunnel_control", "tunnel_data"}}
}
func (f applicationServiceGrantFixture) body() map[string]string {
	return map[string]string{"daemon_id": f.daemon}
}

func newApplicationServiceGrantFixture(t *testing.T, daemonOverrides ...string) applicationServiceGrantFixture {
	t.Helper()
	if testPool == nil {
		t.Fatal("owned managed PostgreSQL required")
	}
	user := dbfx.User(t, "Application service owner", "application-service-"+uuid.NewString()+"@example.test")
	workspace := dbfx.Workspace(t, "Application service workspace", "application-service-"+uuid.NewString())
	member := dbfx.Member(t, workspace, user, "owner")
	fx := testutil.New(testPool, workspace, user)
	daemonID := uuid.NewString()
	if len(daemonOverrides) > 0 {
		daemonID = daemonOverrides[0]
	}
	runtimeID := fx.Runtime(t, "application child runtime", testutil.Cols{"daemon_id": daemonID, "runtime_mode": "local", "metadata": testutil.Raw(`'{"capabilities":["applications-v1"]}'::jsonb`)})
	control, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatal(err)
	}
	fx.Insert(t, "daemon_token", testutil.Cols{"token_hash": auth.HashToken(control), "workspace_id": workspace, "daemon_id": daemonID, "expires_at": time.Now().Add(time.Hour)})
	pat, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatal(err)
	}
	fx.Insert(t, "personal_access_token", testutil.Cols{"user_id": user, "name": "fixture application parent", "token_hash": auth.HashToken(pat), "token_prefix": pat[:8], "expires_at": time.Now().Add(time.Hour)})
	process, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "http://127.0.0.1", Account: user, Profile: t.TempDir(), DaemonID: daemonID, Service: "application"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.ApplicationGateway = applicationgateway.NewHub()
	f := applicationServiceGrantFixture{fx: fx, handler: &h, router: applicationServiceTestRouter(&h), workspace: workspace, user: user, member: member, runtime: runtimeID, daemon: daemonID, control: control, pat: pat, instance: process.InstanceID}
	fx.Cleanup(t, "DELETE FROM application_service_authority WHERE runtime_id=$1", runtimeID)
	fx.Cleanup(t, "DELETE FROM application_service_grant WHERE runtime_id=$1", runtimeID)
	t.Cleanup(h.ApplicationGateway.Close)
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), control, f.grantInput(0))).Want(200).JSON(&f.grant)
	return f
}

func (f applicationServiceGrantFixture) createApp(t *testing.T) application.View {
	t.Helper()
	project := f.fx.Project(t, "fixture app project")
	config := protocol.DefaultApplicationConfig()
	config.Mode = "external"
	config.Port = 4100
	actor := application.Actor{Type: "member", ID: parseUUID(f.user), UserID: parseUUID(f.user)}
	app, err := f.handler.applicationService().Create(context.Background(), parseUUID(f.workspace), application.CreateInput{ProjectID: parseUUID(project), Name: "fixture service", Kind: "service", Config: config}, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"DELETE FROM application WHERE id=$1", "DELETE FROM application_revision WHERE application_id=$1", "DELETE FROM application_relation WHERE source_id=$1 OR target_id=$1", "DELETE FROM application_instance WHERE application_id=$1", "DELETE FROM application_operation WHERE application_id=$1", "DELETE FROM application_operation_step WHERE operation_id IN (SELECT id FROM application_operation WHERE application_id=$1)", "DELETE FROM application_instance_consumer WHERE root_application_id=$1", "DELETE FROM application_endpoint WHERE application_id=$1"} {
		f.fx.Cleanup(t, query, app.ID)
	}
	return app
}

func (f applicationServiceGrantFixture) app(t *testing.T) (application.View, application.OperationView) {
	t.Helper()
	app := f.createApp(t)
	actor := application.Actor{Type: "member", ID: parseUUID(f.user), UserID: parseUUID(f.user)}
	operation, err := f.handler.applicationService().Enqueue(context.Background(), parseUUID(f.workspace), parseUUID(app.ID), application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: f.runtime, IdempotencyKey: uuid.NewString()}, actor)
	if err != nil {
		t.Fatal(err)
	}
	return app, operation
}

func TestApplicationServiceGrantScopeAndHashOnlyStorage(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	if len(f.instance) != 32 || f.grant.ServiceInstanceID != uuid.MustParse(f.instance).String() {
		t.Fatalf("process identity normalization: %q -> %q", f.instance, f.grant.ServiceInstanceID)
	}
	if f.grant.Generation != 1 || time.Until(f.grant.ExpiresAt) > 15*time.Minute || time.Until(f.grant.ExpiresAt) < 14*time.Minute {
		t.Fatal("grant expiry or generation is not bounded")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM application_service_grant WHERE token_hash=$1", f.grant.Token); count != 0 {
		t.Fatal("plaintext token stored")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM application_service_grant WHERE token_hash=$1", auth.HashToken(f.grant.Token)); count != 1 {
		t.Fatal("missing grant hash")
	}
	for _, tc := range []struct {
		method, path, token string
		body                any
		want                int
	}{
		{"POST", f.path("/applications/sync"), f.grant.Token, f.body(), 200},
		{"GET", f.path("/applications/sync"), f.grant.Token, nil, 403},
		{"POST", f.path("/applications/tunnel/control"), f.grant.Token, f.body(), 403},
		{"POST", f.path("/execution-supervisor"), f.grant.Token, map[string]any{}, 403},
		{"POST", "/api/daemon/tasks/" + uuid.NewString() + "/start", f.grant.Token, map[string]any{}, 403},
		{"POST", f.path("/application-service-grants"), f.grant.Token, f.grantInput(1), 403},
		{"POST", "/api/daemon/runtimes/" + uuid.NewString() + "/applications/sync", f.grant.Token, f.body(), 403},
		{"POST", f.path("/applications/sync/extra"), f.grant.Token, f.body(), 403},
		{"GET", "/api/account", f.grant.Token, nil, 401},
		{"POST", "/api/application-connections/resolve", f.grant.Token, nil, 401},
		{"POST", f.path("/applications/sync"), "mps_" + strings.Repeat("a", 64), f.body(), 401},
		{"POST", f.path("/applications/sync"), "mps_short", f.body(), 401},
		{"POST", f.path("/applications/sync"), f.grant.Token, map[string]string{"daemon_id": uuid.NewString()}, 403},
	} {
		testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest(tc.method, tc.path, tc.token, tc.body)).Want(tc.want)
	}
	invalid := f.grantInput(1)
	invalid.Operations = []string{"tasks"}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), f.control, invalid)).Want(400)
	if count := f.fx.Count(t, "SELECT count(*) FROM application_service_grant WHERE runtime_id=$1", f.runtime); count != 1 {
		t.Fatal("invalid scope changed grants")
	}
}

func TestApplicationServiceGrantLifecyclePreservesOperationGuardsAndLegacyClients(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	_, operation := f.app(t)
	for _, token := range []string{f.control, f.pat, f.grant.Token} {
		var registry []protocol.ApplicationRuntimeInstance
		testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), token, f.body())).Want(200).JSON(&registry)
		if len(registry) != 1 {
			t.Fatalf("legacy/scoped registry count=%d", len(registry))
		}
	}
	var claims []protocol.ApplicationClaim
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/claim"), f.grant.Token, f.body())).Want(200).JSON(&claims)
	if len(claims) != 1 || claims[0].OperationID != operation.ID {
		t.Fatalf("claim count=%d; expected operation=%s", len(claims), operation.ID)
	}
	claim := claims[0]
	lease := map[string]string{"daemon_id": f.daemon, "claim_token": claim.ClaimToken}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/steps/"+claim.StepID+"/lease"), f.grant.Token, lease)).Want(204)
	lease["claim_token"] = uuid.NewString()
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/steps/"+claim.StepID+"/lease"), f.grant.Token, lease)).Want(409)
	observation := protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/observe"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "observation": observation})).Want(204)
	stale := observation
	stale.Generation++
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/observe"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "observation": stale})).Want(409)
	result := protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "completed", Observation: observation}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/steps/"+claim.StepID+"/result"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "result": result})).Want(200)
	}
	var state string
	f.fx.QueryRow(t, "SELECT state FROM application_operation_step WHERE id=$1", claim.StepID).Scan(&state)
	if state != "completed" {
		t.Fatalf("step state=%s", state)
	}
	other := newApplicationServiceGrantFixture(t)
	_, _ = other.app(t)
	var foreign []protocol.ApplicationClaim
	testutil.Call(t, other.router.ServeHTTP, applicationServiceRequest("POST", other.path("/applications/claim"), other.grant.Token, other.body())).Want(200).JSON(&foreign)
	observation.InstanceID = foreign[0].Command.InstanceID
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/observe"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "observation": observation})).Want(404)
}

func TestApplicationServiceGrantRotationAndRevokeAreGenerationFenced(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	var group sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			input := f.grantInput(1)
			input.ServiceInstanceID = uuid.NewString()
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, applicationServiceRequest("POST", f.path("/application-service-grants"), f.control, input))
			responses <- w
		}()
	}
	group.Wait()
	close(responses)
	winners := 0
	var current protocol.ApplicationServiceGrantResponse
	for response := range responses {
		if response.Code == 200 {
			winners++
			if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil {
				t.Fatal(err)
			}
		} else if response.Code != 409 {
			t.Fatalf("rotation status=%d", response.Code)
		}
	}
	if winners != 1 || current.Generation != 2 {
		t.Fatalf("rotation winners=%d generation=%d", winners, current.Generation)
	}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), f.grant.Token, f.body())).Want(401)
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants/revoke"), f.control, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: f.instance, Generation: 1})).Want(409)
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), current.Token, f.body())).Want(200)
	revoke := protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: current.ServiceInstanceID, Generation: 2}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants/revoke"), f.control, revoke)).Want(204)
	}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), current.Token, f.body())).Want(401)
	var state protocol.ApplicationServiceGrantState
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("GET", f.path("/application-service-grants"), f.control, nil)).Want(200).JSON(&state)
	if !state.Revoked || state.Generation != 2 {
		t.Fatalf("authority metadata=%+v", state)
	}
}

func TestApplicationServiceGrantRevocationCannotReviveAfterMemberRejoin(t *testing.T) {
	for _, scenario := range []string{"expiry", "membership", "owner", "daemon", "runtime"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApplicationServiceGrantFixture(t)
			switch scenario {
			case "expiry":
				f.fx.Exec(t, "UPDATE application_service_grant SET expires_at=now()-interval '1 second' WHERE runtime_id=$1", f.runtime)
			case "membership":
				f.fx.Exec(t, "DELETE FROM member WHERE id=$1", f.member)
				f.fx.Member(t, f.workspace, f.user, "owner")
			case "owner":
				owner := f.fx.User(t, "replacement owner", "replacement-"+uuid.NewString()+"@example.test")
				f.fx.Cleanup(t, "UPDATE agent_runtime SET owner_id=$2 WHERE id=$1", f.runtime, f.user)
				f.fx.Exec(t, "UPDATE agent_runtime SET owner_id=$2 WHERE id=$1", f.runtime, owner)
			case "daemon":
				f.fx.Exec(t, "UPDATE agent_runtime SET daemon_id=$2 WHERE id=$1", f.runtime, uuid.NewString())
			case "runtime":
				f.fx.Exec(t, "DELETE FROM agent_runtime WHERE id=$1", f.runtime)
			}
			testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), f.grant.Token, f.body())).Want(401)
		})
	}
}

func TestApplicationServiceGrantRevokeSerializesWithBusinessWrite(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	_, operation := f.app(t)
	var instance db.ApplicationInstance
	var instanceID string
	f.fx.QueryRow(t, "SELECT instance_id FROM application_operation_step WHERE operation_id=$1", operation.ID).Scan(&instanceID)
	instance, err := f.handler.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: parseUUID(instanceID), WorkspaceID: parseUUID(f.workspace)})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "UPDATE application_service_authority SET revoked=true WHERE runtime_id=$1", f.runtime); err != nil {
		t.Fatal(err)
	}
	observation := protocol.ApplicationObservation{InstanceID: instanceID, Generation: instance.Generation, Revision: instance.Revision, ProcessState: "running", HealthState: "healthy"}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, applicationServiceRequest("POST", f.path("/applications/observe"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "observation": observation}))
		response <- w
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		f.fx.QueryRow(t, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockApplicationServiceGrant%')").Scan(&waiting)
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("callback did not wait on authority transaction")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-response:
		if got.Code != 401 {
			t.Fatalf("stale business write=%d %s", got.Code, got.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback remained blocked")
	}
	var state string
	f.fx.QueryRow(t, "SELECT process_state FROM application_instance WHERE id=$1", instanceID).Scan(&state)
	if state != instance.ProcessState {
		t.Fatalf("revoked request wrote process state=%s", state)
	}
}

func TestApplicationServiceGrantAcceptsTextDaemonOverrides(t *testing.T) {
	const override = "lab-mac.local:build-profile"
	f := newApplicationServiceGrantFixture(t, override)
	if f.grant.DaemonID != override {
		t.Fatalf("daemon override changed: %q", f.grant.DaemonID)
	}
	for _, token := range []string{f.control, f.pat, f.grant.Token} {
		var registry []protocol.ApplicationRuntimeInstance
		testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), token, f.body())).Want(200).JSON(&registry)
		if len(registry) != 0 {
			t.Fatal("text daemon grant returned unexpected registry")
		}
	}
	server := f.server(t)
	connection := f.dial(t, server, "control", f.grant.Token)
	connection.Close()
}

func TestApplicationServiceGrantExpiryRollsBackInFlightClaim(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	app, operation := f.app(t)
	f.fx.Exec(t, "UPDATE application_service_grant SET expires_at=clock_timestamp()+interval '750 milliseconds' WHERE token_hash=$1", auth.HashToken(f.grant.Token))
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "SELECT id FROM project WHERE id=$1 FOR UPDATE", app.ProjectID); err != nil {
		t.Fatal(err)
	}
	var before string
	f.fx.QueryRow(t, "SELECT state FROM application_operation_step WHERE operation_id=$1", operation.ID).Scan(&before)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, applicationServiceRequest("POST", f.path("/applications/claim"), f.grant.Token, f.body()))
		response <- w
	}()
	deadline := time.Now().Add(4 * time.Second)
	for {
		var waiting bool
		f.fx.QueryRow(t, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockRuntimeApplicationProjects%')").Scan(&waiting)
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim did not reach business lock after grant validation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for {
		var expired bool
		f.fx.QueryRow(t, "SELECT expires_at<clock_timestamp() FROM application_service_grant WHERE token_hash=$1", auth.HashToken(f.grant.Token)).Scan(&expired)
		if expired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grant expiry did not advance")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-response:
		if got.Code != 401 {
			t.Fatalf("expired in-flight claim=%d %s", got.Code, got.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not finish after releasing lock")
	}
	var after string
	var claimed bool
	f.fx.QueryRow(t, "SELECT state,claim_token IS NOT NULL FROM application_operation_step WHERE operation_id=$1", operation.ID).Scan(&after, &claimed)
	if after != before || claimed {
		t.Fatalf("expired request committed business state: %s -> %s claimed=%t", before, after, claimed)
	}
}

func TestApplicationServiceGrantPreservesCatalogStableDaemonConstraint(t *testing.T) {
	f := newApplicationServiceGrantFixture(t, "legacy-host:profile")
	app := f.createApp(t)
	actor := application.Actor{Type: "member", ID: parseUUID(f.user), UserID: parseUUID(f.user)}
	_, err := f.handler.applicationService().Enqueue(context.Background(), parseUUID(f.workspace), parseUUID(app.ID), application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: f.runtime, IdempotencyKey: uuid.NewString()}, actor)
	if !errors.Is(err, application.ErrConflict) || !strings.Contains(err.Error(), "stable daemon identity") {
		t.Fatalf("existing catalog boundary changed: %v", err)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM application_instance WHERE application_id=$1", app.ID); count != 0 {
		t.Fatal("unsupported daemon identity created app instance")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM application_operation WHERE application_id=$1", app.ID); count != 0 {
		t.Fatal("unsupported daemon identity committed app operation")
	}
}

func TestApplicationServiceGrantParentOwnershipAndRuntimeBoundary(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	sibling := f.fx.Runtime(t, "other app runtime", testutil.Cols{"daemon_id": f.daemon, "provider": "opencode", "metadata": testutil.Raw(`'{"capabilities":["applications-v1"]}'::jsonb`)})
	app := f.createApp(t)
	actor := application.Actor{Type: "member", ID: parseUUID(f.user), UserID: parseUUID(f.user)}
	operation, err := f.handler.applicationService().Enqueue(context.Background(), parseUUID(f.workspace), parseUUID(app.ID), application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: sibling, IdempotencyKey: uuid.NewString()}, actor)
	if err != nil {
		t.Fatal(err)
	}
	step := operation.Steps[0]
	observation := protocol.ApplicationObservation{InstanceID: step.InstanceID, Generation: 1, Revision: app.Revision, ProcessState: "running", HealthState: "healthy"}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/observe"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "observation": observation})).Want(403)
	result := protocol.ApplicationStepResult{ClaimToken: uuid.NewString(), State: "completed", Observation: observation}
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/steps/"+step.ID+"/result"), f.grant.Token, map[string]any{"daemon_id": f.daemon, "result": result})).Want(409)
	otherDaemonToken, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatal(err)
	}
	f.fx.Insert(t, "daemon_token", testutil.Cols{"token_hash": auth.HashToken(otherDaemonToken), "workspace_id": f.workspace, "daemon_id": uuid.NewString(), "expires_at": time.Now().Add(time.Hour)})
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), otherDaemonToken, f.grantInput(1))).Want(403)
	otherUser := f.fx.User(t, "other workspace administrator", "app-grant-other-"+uuid.NewString()+"@example.test")
	f.fx.Member(t, f.workspace, otherUser, "admin")
	otherPAT, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatal(err)
	}
	f.fx.Insert(t, "personal_access_token", testutil.Cols{"user_id": otherUser, "name": "other fixture parent", "token_hash": auth.HashToken(otherPAT), "token_prefix": otherPAT[:8], "expires_at": time.Now().Add(time.Hour)})
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), otherPAT, f.grantInput(1))).Want(403)
	var rotated protocol.ApplicationServiceGrantResponse
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), f.pat, f.grantInput(1))).Want(200).JSON(&rotated)
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), rotated.Token, f.body())).Want(200)
	var stepState string
	f.fx.QueryRow(t, "SELECT state FROM application_operation_step WHERE id=$1", step.ID).Scan(&stepState)
	if stepState != "queued" {
		t.Fatalf("foreign application step mutated: %s", stepState)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM application_service_grant WHERE runtime_id=$1", f.runtime); count != 2 {
		t.Fatalf("unauthorized parent minted grant: count=%d", count)
	}
}
