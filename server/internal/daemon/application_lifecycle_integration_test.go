package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationFullLifecycleUsesRealAPIHostAndRemoteGateway(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("database unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	fx := testutil.New(pool, "", "")
	identity := uuid.NewString()
	userID := fx.User(t, "application lifecycle member", "application-lifecycle-"+identity+"@example.com")
	workspaceID := fx.Workspace(t, "application lifecycle", "application-lifecycle-"+identity)
	fx.WorkspaceID = workspaceID
	fx.UserID = userID
	fx.Member(t, workspaceID, userID, "owner")
	projectID := fx.Project(t, "application lifecycle project")
	queries := db.New(pool)
	h := handler.New(queries, pool, nil, nil, service.NewEmailService(), nil, nil, analytics.NoopClient{}, handler.Config{ApplicationOrigin: "http://apps.localhost"})
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-User-ID", userID)
			r.Header.Set("X-Workspace-ID", workspaceID)
			next.ServeHTTP(w, r)
		})
	})
	router.Use(h.ApplicationHostBoundary)
	router.Post("/api/application-connections/resolve", h.ResolveApplicationConnection)
	router.Post("/api/applications/{id}/operations", h.EnqueueApplicationOperation)
	router.Get("/api/applications/{id}/operations/{operationId}", h.GetApplicationOperation)
	router.Post("/api/applications/{id}/endpoints/{endpointId}/service-access", h.CreateApplicationServiceAccess)
	router.Get("/api/applications/{id}/instances/{instanceId}/logs", h.GetApplicationLogs)
	router.Post("/api/daemon/runtimes/{runtimeId}/applications/sync", h.SyncRuntimeApplications)
	router.Post("/api/daemon/runtimes/{runtimeId}/applications/claim", h.ClaimRuntimeApplications)
	router.Post("/api/daemon/runtimes/{runtimeId}/applications/observe", h.ReportRuntimeApplication)
	router.Post("/api/daemon/runtimes/{runtimeId}/applications/steps/{stepId}/lease", h.RenewRuntimeApplicationLease)
	router.Post("/api/daemon/runtimes/{runtimeId}/applications/steps/{stepId}/result", h.CompleteRuntimeApplication)
	router.Get("/api/daemon/runtimes/{runtimeId}/applications/tunnel/control", h.ConnectApplicationControl)
	router.Get("/api/daemon/runtimes/{runtimeId}/applications/tunnel/data", h.ConnectApplicationStream)
	server := httptest.NewServer(router)
	defer server.Close()
	d, command := applicationManagerFixture(t, server.URL)
	runtimeID := fx.Runtime(t, "application lifecycle runtime", testutil.Cols{"daemon_id": d.cfg.DaemonID, "metadata": testutil.Raw(`'{"capabilities":["applications-v1"]}'::jsonb`), "status": "online", "last_seen_at": time.Now()})
	d.runtimeIndex = map[string]Runtime{runtimeID: {ID: runtimeID, Status: "online"}}
	d.workspaces = map[string]*workspaceState{workspaceID: {workspaceID: workspaceID, runtimeIDs: []string{runtimeID}}}
	d.applicationServerCapabilities.Delete(command.RuntimeID)
	d.applicationServerCapabilities.Store(runtimeID, true)
	resourceID := fx.Insert(t, "project_resource", testutil.Cols{"workspace_id": workspaceID, "project_id": projectID, "resource_type": "local_directory", "resource_ref": command.ResourceRef})
	command.Config.ResourceID = resourceID
	command.Config.AutoPublish = true
	parse := func(raw string) pgtype.UUID {
		id, err := util.ParseUUID(raw)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	svc := &application.Service{Queries: queries, Transactions: pool}
	app, err := svc.Create(ctx, parse(workspaceID), application.CreateInput{ProjectID: parse(projectID), Name: "lifecycle service", Kind: "service", Config: command.Config}, application.Actor{Type: "member", ID: parse(userID), UserID: parse(userID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"application", "application_revision", "application_relation", "application_instance", "application_endpoint", "application_operation", "application_operation_step", "application_instance_consumer", "application_access_ticket"} {
		fx.Cleanup(t, "DELETE FROM "+table+" WHERE workspace_id=$1", workspaceID)
	}
	t.Cleanup(d.stopOwnedApplications)
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	loopCtx, stopLoops := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); d.serveApplicationTunnel(loopCtx, runtimeID) }()
	defer func() { stopLoops(); <-done }()
	request := func(method, path string, body any, out any) {
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = strings.NewReader(string(raw))
		}
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			raw, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s failed: %d %s", method, path, response.StatusCode, raw)
		}
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var operation application.OperationView
	request(http.MethodPost, "/api/applications/"+app.ID+"/operations", map[string]any{"action": "start", "revision": app.Revision, "runtime_id": runtimeID, "idempotency_key": "lifecycle-start"}, &operation)
	d.pollRuntimeApplications(ctx, semaphore, &workers, runtimeID)
	workers.Wait()
	request(http.MethodGet, "/api/applications/"+app.ID+"/operations/"+operation.ID, nil, &operation)
	if operation.State != "completed" {
		t.Fatalf("real service was not ready: %+v", operation)
	}
	endpoints, err := queries.ListApplicationEndpoints(ctx, db.ListApplicationEndpointsParams{WorkspaceID: parse(workspaceID), ApplicationID: parse(app.ID)})
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("service was not published: %+v %v", endpoints, err)
	}
	waitApplicationManager(t, func() bool { return h.ApplicationGateway.Connected(runtimeID) })
	var access struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	request(http.MethodPost, "/api/applications/"+app.ID+"/endpoints/"+util.UUIDToString(endpoints[0].ID)+"/service-access", nil, &access)
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = util.UUIDToString(endpoints[0].ID) + ".apps.localhost"
	r.Header.Set("Authorization", "Bearer "+access.Token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "managed application response" {
		t.Fatalf("remote service access=%d %q %v", response.StatusCode, body, err)
	}
	var logs struct {
		Text string `json:"text"`
	}
	request(http.MethodGet, "/api/applications/"+app.ID+"/instances/"+operation.Steps[0].InstanceID+"/logs", nil, &logs)
	if !strings.Contains(logs.Text, "application service started") {
		t.Fatalf("real service logs missing: %q", logs.Text)
	}
	consumerDaemon, consumerCommand := applicationManagerFixture(t, server.URL)
	consumerDaemon.cfg.DaemonID = uuid.NewString()
	consumerRuntimeID := fx.Runtime(t, "application consumer runtime", testutil.Cols{"daemon_id": consumerDaemon.cfg.DaemonID, "metadata": testutil.Raw(`'{"capabilities":["applications-v1"]}'::jsonb`), "status": "online", "last_seen_at": time.Now()})
	consumerDaemon.runtimeIndex = map[string]Runtime{consumerRuntimeID: {ID: consumerRuntimeID, Status: "online"}}
	consumerDaemon.workspaces = map[string]*workspaceState{workspaceID: {workspaceID: workspaceID, runtimeIDs: []string{consumerRuntimeID}}}
	consumerDaemon.applicationServerCapabilities.Delete(consumerCommand.RuntimeID)
	consumerDaemon.applicationServerCapabilities.Store(consumerRuntimeID, true)
	consumerTunnelDone := make(chan struct{})
	go func() {
		defer close(consumerTunnelDone)
		consumerDaemon.serveApplicationTunnel(loopCtx, consumerRuntimeID)
	}()
	defer func() { stopLoops(); <-consumerTunnelDone }()
	var sourceRef localDirectoryRef
	if err := json.Unmarshal(consumerCommand.ResourceRef, &sourceRef); err != nil {
		t.Fatal(err)
	}
	sourceRef.DaemonID = consumerDaemon.cfg.DaemonID
	consumerCommand.ResourceRef, err = json.Marshal(sourceRef)
	if err != nil {
		t.Fatal(err)
	}
	consumerResourceID := fx.Insert(t, "project_resource", testutil.Cols{"workspace_id": workspaceID, "project_id": projectID, "resource_type": "local_directory", "resource_ref": consumerCommand.ResourceRef})
	consumerCommand.Config.ResourceID = consumerResourceID
	consumerCommand.Config.Connections = []protocol.ApplicationConnection{{TargetID: app.ID, URLVariable: "API_URL"}}
	consumerCommand.Config.Environment["APPLICATION_FIXTURE_GATEWAY_ADDR"] = server.Listener.Addr().String()
	consumer, err := svc.Create(ctx, parse(workspaceID), application.CreateInput{ProjectID: parse(projectID), Name: "cross-runtime consumer", Kind: "service", Config: consumerCommand.Config}, application.Actor{Type: "member", ID: parse(userID), UserID: parse(userID)})
	if err != nil {
		t.Fatal(err)
	}
	connections := []application.Relation{{TargetID: app.ID, Type: "depends_on", Condition: "healthy", Required: true}}
	consumer, err = svc.Update(ctx, parse(workspaceID), parse(consumer.ID), application.UpdateInput{Revision: consumer.Revision, Relations: &connections}, application.Actor{Type: "member", ID: parse(userID), UserID: parse(userID)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumerDaemon.stopOwnedApplications)
	request(http.MethodPost, "/api/applications/"+consumer.ID+"/operations", map[string]any{"action": "start", "revision": consumer.Revision, "runtime_id": consumerRuntimeID, "placements": map[string]string{app.ID: runtimeID}, "idempotency_key": "consumer-start"}, &operation)
	consumerDaemon.pollRuntimeApplications(ctx, semaphore, &workers, consumerRuntimeID)
	workers.Wait()
	request(http.MethodGet, "/api/applications/"+consumer.ID+"/operations/"+operation.ID, nil, &operation)
	if operation.State != "completed" {
		t.Fatalf("cross-runtime consumer was not ready: %+v", operation)
	}
	consumerInstanceID := operation.Steps[0].InstanceID
	waitApplicationManager(t, func() bool { return h.ApplicationGateway.Connected(consumerRuntimeID) })
	dependencyURL := "http://127.0.0.1:" + strconv.Itoa(consumerCommand.Config.Port) + "/dependency"
	response, err = (&http.Client{Timeout: 8 * time.Second}).Get(dependencyURL)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "managed application response" {
		t.Fatalf("cross-runtime binding response=%d %q error=%v", response.StatusCode, body, err)
	}
	request(http.MethodGet, "/api/applications/"+consumer.ID+"/instances/"+consumerInstanceID+"/logs", nil, &logs)
	if !strings.Contains(logs.Text, "application service started") {
		t.Fatal("connection credential delayed ordinary live logs")
	}
	uploadReader, uploadWriter := io.Pipe()
	defer uploadReader.Close()
	defer uploadWriter.Close()
	uploadRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, dependencyURL+"/upload", uploadReader)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(uploaded.Body, ready); err != nil || string(ready) != "ready\n" {
		uploaded.Body.Close()
		t.Fatalf("complete dependency chain waited for upload completion: %q %v", ready, err)
	}
	payload := bytes.Repeat([]byte("real dependency payload\n"), 50000)
	writeDone := make(chan error, 1)
	go func() {
		_, err := uploadWriter.Write(payload)
		if err == nil {
			err = uploadWriter.Close()
		}
		writeDone <- err
	}()
	downloaded, err := io.ReadAll(uploaded.Body)
	uploaded.Body.Close()
	if err != nil || !bytes.Equal(downloaded, payload) {
		t.Fatalf("complete dependency chain lost transfer bytes: size=%d error=%v", len(downloaded), err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	eventRequest, err := http.NewRequestWithContext(eventCtx, http.MethodGet, dependencyURL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := http.DefaultClient.Do(eventRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	eventReady := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(events.Body, eventReady); err != nil || string(eventReady) != "data: ready\n\n" {
		t.Fatalf("complete dependency chain did not flush SSE: %q %v", eventReady, err)
	}
	socket, socketResponse, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(dependencyURL, "http")+"/socket", nil)
	if socketResponse != nil {
		socketResponse.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteMessage(websocket.TextMessage, []byte("real dependency socket")); err != nil {
		t.Fatal(err)
	}
	socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, echo, err := socket.ReadMessage()
	if err != nil || string(echo) != "real dependency socket" {
		t.Fatalf("complete dependency chain did not echo WebSocket: %q %v", echo, err)
	}
	request(http.MethodPost, "/api/applications/"+app.ID+"/operations", map[string]any{"action": "unpublish", "revision": app.Revision, "runtime_id": runtimeID, "idempotency_key": "dependency-unpublish"}, &operation)
	d.pollRuntimeApplications(ctx, semaphore, &workers, runtimeID)
	workers.Wait()
	response, err = (&http.Client{Timeout: 8 * time.Second}).Get(dependencyURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("cross-runtime connection survived dependency publication revocation")
	}
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("revoked dependency WebSocket remained open across the full service chain")
	}
	eventClosed := make(chan error, 1)
	go func() { _, err := io.ReadAll(events.Body); eventClosed <- err }()
	select {
	case <-eventClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked dependency SSE remained open across the full service chain")
	}
	request(http.MethodPost, "/api/applications/"+consumer.ID+"/operations", map[string]any{"action": "stop", "revision": consumer.Revision, "runtime_id": consumerRuntimeID, "idempotency_key": "consumer-stop"}, &operation)
	consumerDaemon.pollRuntimeApplications(ctx, semaphore, &workers, consumerRuntimeID)
	workers.Wait()
	request(http.MethodGet, "/api/applications/"+consumer.ID+"/operations/"+operation.ID, nil, &operation)
	if operation.State != "completed" {
		t.Fatalf("cross-runtime consumer shutdown was not confirmed: %+v", operation)
	}
	request(http.MethodPost, "/api/applications/"+app.ID+"/operations", map[string]any{"action": "stop", "revision": app.Revision, "runtime_id": runtimeID, "idempotency_key": "lifecycle-stop"}, &operation)
	if err := queries.SetApplicationOperationDeadline(ctx, db.SetApplicationOperationDeadlineParams{ID: parse(operation.ID), WorkspaceID: parse(workspaceID), DeadlineAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	expired, err := svc.ExpireOperations(ctx, time.Now(), 16)
	if err != nil || len(expired) != 1 || expired[0].ID != operation.ID || expired[0].State != "failed" {
		t.Fatalf("real service stop timeout did not settle its operation: count=%d error=%v", len(expired), err)
	}
	d.pollRuntimeApplications(ctx, semaphore, &workers, runtimeID)
	workers.Wait()
	request(http.MethodGet, "/api/applications/"+app.ID+"/operations/"+operation.ID, nil, &operation)
	if operation.State != "failed" {
		t.Fatalf("reconnect incorrectly changed timeout into operation success: state=%s", operation.State)
	}
	registry, err := svc.RuntimeInstances(ctx, parse(workspaceID), parse(runtimeID))
	if err != nil || len(registry) != 1 || !registry[0].ConfirmedStopped || registry[0].HasPendingOperation {
		t.Fatalf("timed-out stop did not confirm actual process exit on reconnect: %v", err)
	}
}
