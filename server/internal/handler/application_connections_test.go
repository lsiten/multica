package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationConnectionRevokesSessionsWhenEitherRuntimeAccessChanges(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, revoked := range []string{"source", "target"} {
		t.Run(revoked, func(t *testing.T) {
			targetEndpoint, targetRuntime := applicationAccessFixture(t)
			sourceApp := applicationTestCreate(t, dbfx.Project(t, "application connection source"), "source", "service")
			sourceRuntime := dbfx.Runtime(t, "connection source runtime", testutil.Cols{"daemon_id": uuid.NewString()})
			sourceInstanceID := dbfx.Insert(t, "application_instance", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": sourceApp.ID, "runtime_id": sourceRuntime, "daemon_id": uuid.NewString(), "revision": 1, "generation": 1, "desired_state": "running"})
			h := *testHandler
			h.cfg.ApplicationOrigin = "http://apps.localhost"
			origin, _, err := h.applicationOrigin()
			if err != nil {
				t.Fatal(err)
			}
			grant := applicationgateway.ConnectionGrant{WorkspaceID: testWorkspaceID, MemberID: applicationTestMemberID(t, testUserID), SourceInstanceID: sourceInstanceID, SourceGeneration: 1, TargetInstanceID: uuidToString(targetEndpoint.InstanceID), TargetGeneration: 1, RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}}
			token, err := applicationgateway.SignConnectionGrant(auth.JWTSecret(), grant)
			if err != nil {
				t.Fatal(err)
			}
			request := func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/api/application-connections/resolve", nil)
				r.Header.Set("Authorization", "Bearer "+token)
				return r
			}
			var access struct {
				URL   string `json:"url"`
				Token string `json:"token"`
			}
			testutil.Call(t, h.ResolveApplicationConnection, request()).Want(http.StatusOK).JSON(&access)
			active := httptest.NewRequest(http.MethodGet, access.URL, nil)
			active.AddCookie(&http.Cookie{Name: "multica_app_session", Value: access.Token})
			if _, allowed := h.applicationAccessAllowed(active, origin, targetEndpoint); !allowed {
				t.Fatal("new dependency session was denied")
			}
			ownerID := createPlainMember(t, "application-connection-owner-"+uuid.NewString()+"@multica.test")
			runtimeID := sourceRuntime
			if revoked == "target" {
				runtimeID = targetRuntime
			}
			if _, err := testPool.Exec(context.Background(), "UPDATE agent_runtime SET owner_id=$1,visibility='private' WHERE id=$2", ownerID, runtimeID); err != nil {
				t.Fatal(err)
			}
			denied := httptest.NewRecorder()
			h.ResolveApplicationConnection(denied, request())
			if denied.Code != http.StatusForbidden {
				t.Errorf("%s runtime access was revoked but issued a new session: %d", revoked, denied.Code)
			}
			if _, allowed := h.applicationAccessAllowed(active, origin, targetEndpoint); allowed {
				t.Errorf("existing dependency session survived revoked %s runtime access", revoked)
			}
		})
	}
}

func TestApplicationDependencySessionRemainsRevokedAfterInstanceGenerationReplacement(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	endpoint, _ := applicationAccessFixture(t)
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost"
	origin, _, err := h.applicationOrigin()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := h.Queries.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: endpoint.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	grant := applicationgateway.ConnectionGrant{WorkspaceID: testWorkspaceID, MemberID: applicationTestMemberID(t, testUserID), SourceInstanceID: uuidToString(instance.ID), SourceGeneration: instance.Generation, TargetInstanceID: uuidToString(instance.ID), TargetGeneration: instance.Generation, RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}}
	session, err := applicationgateway.SignConnectionAccess(auth.JWTSecret(), uuidToString(endpoint.ID), endpoint.Revision, grant)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+uuidToString(endpoint.ID)+".apps.localhost/", nil)
	request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
	if _, allowed := h.applicationAccessAllowed(request, origin, endpoint); !allowed {
		t.Fatal("original instance session was denied")
	}
	replacement, err := h.Queries.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: endpoint.WorkspaceID, DesiredState: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := h.applicationAccessAllowed(request, origin, endpoint); allowed {
		t.Fatal("old dependency session survived real instance replacement")
	}
	resolve := func(grant applicationgateway.ConnectionGrant) *httptest.ResponseRecorder {
		t.Helper()
		token, err := applicationgateway.SignConnectionGrant(auth.JWTSecret(), grant)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/application-connections/resolve", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		h.ResolveApplicationConnection(out, r)
		return out
	}
	if response := resolve(grant); response.Code != http.StatusForbidden {
		t.Fatalf("old generation refreshed its dependency authorization: %d", response.Code)
	}
	grant.SourceGeneration, grant.TargetGeneration = replacement.Generation, replacement.Generation
	response := resolve(grant)
	if response.Code != http.StatusOK {
		t.Fatalf("replacement generation could not obtain fresh authorization: %d", response.Code)
	}
	var access struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(strings.NewReader(response.Body.String())).Decode(&access); err != nil {
		t.Fatal(err)
	}
	fresh := httptest.NewRequest(http.MethodGet, request.URL.String(), nil)
	fresh.AddCookie(&http.Cookie{Name: "multica_app_session", Value: access.Token})
	if _, allowed := h.applicationAccessAllowed(fresh, origin, endpoint); !allowed {
		t.Fatal("fresh generation-bound session was denied")
	}
}

func TestApplicationConnectionResolverUsesOnlyActiveSourceAndTargetGenerations(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	endpoint, _ := applicationAccessFixture(t)
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost"
	instance, err := h.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: endpoint.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	grant := applicationgateway.ConnectionGrant{WorkspaceID: testWorkspaceID, MemberID: applicationTestMemberID(t, testUserID), SourceInstanceID: uuidToString(instance.ID), SourceGeneration: instance.Generation, TargetInstanceID: uuidToString(instance.ID), TargetGeneration: instance.Generation, RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}}
	request := func(grant applicationgateway.ConnectionGrant) *http.Request {
		token, err := applicationgateway.SignConnectionGrant(auth.JWTSecret(), grant)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/application-connections/resolve", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}
	response := httptest.NewRecorder()
	h.ResolveApplicationConnection(response, request(grant))
	if response.Code != http.StatusOK {
		t.Fatalf("connection denied: %d %s", response.Code, response.Body.String())
	}
	grant.SourceGeneration++
	denied := httptest.NewRecorder()
	h.ResolveApplicationConnection(denied, request(grant))
	if denied.Code != http.StatusForbidden {
		t.Fatal("stale source generation received a dependency credential")
	}
	grant.SourceGeneration--
	grant.TargetGeneration++
	denied = httptest.NewRecorder()
	h.ResolveApplicationConnection(denied, request(grant))
	if denied.Code != http.StatusForbidden {
		t.Fatal("stale target generation received a dependency credential")
	}
}
