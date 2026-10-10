package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationCredentialsDoNotReviveAfterMemberRejoins(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	endpoint, runtimeID := applicationAccessFixture(t)
	if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	userID := createPlainMember(t, "application-rejoin-"+uuid.NewString()+"@multica.test")
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(userID), WorkspaceID: endpoint.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost"
	origin, _, err := h.applicationOrigin()
	if err != nil {
		t.Fatal(err)
	}
	session, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), testWorkspaceID, userID, uuidToString(member.ID), endpoint.Revision)
	if err != nil {
		t.Fatal(err)
	}
	accessRequest := httptest.NewRequest(http.MethodGet, "http://"+uuidToString(endpoint.ID)+".apps.localhost/", nil)
	accessRequest.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
	if _, allowed := h.applicationAccessAllowed(accessRequest, origin, endpoint); !allowed {
		t.Fatal("current membership session was denied")
	}
	instance, err := h.Queries.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: endpoint.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := applicationgateway.SignConnectionGrant(auth.JWTSecret(), applicationgateway.ConnectionGrant{WorkspaceID: testWorkspaceID, MemberID: uuidToString(member.ID), SourceInstanceID: uuidToString(instance.ID), SourceGeneration: instance.Generation, TargetInstanceID: uuidToString(instance.ID), TargetGeneration: instance.Generation, RegisteredClaims: jwt.RegisteredClaims{Subject: userID}})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/application-connections/resolve", nil)
		r.Header.Set("Authorization", "Bearer "+grant)
		response := httptest.NewRecorder()
		h.ResolveApplicationConnection(response, r)
		return response
	}
	if response := resolve(); response.Code != http.StatusOK {
		t.Fatalf("current membership connection was denied: %d", response.Code)
	}
	ticket := strings.Repeat("b", 64)
	if err := h.Queries.CreateApplicationAccessTicket(ctx, db.CreateApplicationAccessTicketParams{TokenHash: auth.HashToken(ticket), EndpointID: endpoint.ID, WorkspaceID: endpoint.WorkspaceID, UserID: parseUUID(userID), MemberID: member.ID, EndpointRevision: endpoint.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.revokeAndRemoveMember(ctx, endpoint.WorkspaceID, parseUUID(userID), member.ID, parseUUID(testUserID)); err != nil {
		t.Fatal(err)
	}
	if _, allowed := h.applicationAccessAllowed(accessRequest, origin, endpoint); allowed {
		t.Fatal("removed member retained application access")
	}
	dbfx.Member(t, testWorkspaceID, userID, "member")
	if _, allowed := h.applicationAccessAllowed(accessRequest, origin, endpoint); allowed {
		t.Error("old application session revived after the member rejoined")
	}
	if response := resolve(); response.Code != http.StatusForbidden {
		t.Errorf("old dependency grant revived after rejoin: %d", response.Code)
	}
	launch := httptest.NewRequest(http.MethodGet, "http://"+uuidToString(endpoint.ID)+".apps.localhost/.__multica/launch?ticket="+ticket, nil)
	response := httptest.NewRecorder()
	h.consumeApplicationLaunch(response, launch, origin, true, endpoint)
	if response.Code == http.StatusSeeOther {
		t.Error("old launch ticket revived after the member rejoined")
	}
	freshSession, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), testWorkspaceID, userID, applicationTestMemberID(t, userID), endpoint.Revision)
	if err != nil {
		t.Fatal(err)
	}
	freshRequest := httptest.NewRequest(http.MethodGet, accessRequest.URL.String(), nil)
	freshRequest.AddCookie(&http.Cookie{Name: "multica_app_session", Value: freshSession})
	if _, allowed := h.applicationAccessAllowed(freshRequest, origin, endpoint); !allowed {
		t.Fatal("newly granted access for the new membership was denied")
	}
}
