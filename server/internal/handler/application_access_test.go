package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func applicationTestMemberID(t *testing.T, userID string) string {
	t.Helper()
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(userID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	return uuidToString(member.ID)
}

func applicationAccessFixture(t *testing.T) (db.ApplicationEndpoint, string) {
	t.Helper()
	projectID := dbfx.Project(t, "application access")
	app := applicationTestCreate(t, projectID, "service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "access-start")
	claim := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, claim, "completed")
	endpoint, err := testHandler.Queries.UpsertApplicationEndpoint(context.Background(), db.UpsertApplicationEndpointParams{WorkspaceID: parseUUID(testWorkspaceID), ApplicationID: parseUUID(app.ID), InstanceID: parseUUID(claim.Command.InstanceID), Port: 4100, EntryPath: "/", Visibility: "workspace", PublishedBy: parseUUID(testUserID), State: "published"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM application_access_ticket WHERE endpoint_id=$1", uuidToString(endpoint.ID))
	return endpoint, runtimeID
}

func TestApplicationLaunchTicketIsSingleUseAndDoesNotCreateManagementSession(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	endpoint, _ := applicationAccessFixture(t)
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost:18608"
	ticket := strings.Repeat("a", 64)
	if err := h.Queries.CreateApplicationAccessTicket(context.Background(), db.CreateApplicationAccessTicketParams{TokenHash: auth.HashToken(ticket), EndpointID: endpoint.ID, WorkspaceID: endpoint.WorkspaceID, UserID: parseUUID(testUserID), MemberID: parseUUID(applicationTestMemberID(t, testUserID)), EndpointRevision: endpoint.Revision}); err != nil {
		t.Fatal(err)
	}
	boundary := h.ApplicationHostBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("application host reached platform router") }))
	url := "http://" + uuidToString(endpoint.ID) + ".apps.localhost:18608/.__multica/launch?ticket=" + ticket
	request := httptest.NewRequest(http.MethodGet, url, nil)
	response := httptest.NewRecorder()
	boundary.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("launch status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "multica_app_session" || !cookies[0].HttpOnly || cookies[0].Domain != "" {
		t.Fatalf("launch cookie was not host-scoped: %+v", cookies)
	}
	claims, err := applicationgateway.ParseAccess(auth.JWTSecret(), cookies[0].Value)
	if err != nil || claims.EndpointID != uuidToString(endpoint.ID) || claims.Subject != testUserID {
		t.Fatalf("access claims=%+v err=%v", claims, err)
	}
	second := httptest.NewRecorder()
	boundary.ServeHTTP(second, request.Clone(context.Background()))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("ticket reused: %d", second.Code)
	}
	platformPath := httptest.NewRequest(http.MethodGet, "http://"+uuidToString(endpoint.ID)+".apps.localhost:18608/api/me", nil)
	unauthorized := httptest.NewRecorder()
	boundary.ServeHTTP(unauthorized, platformPath)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous application host exposed platform API: %d", unauthorized.Code)
	}
}

func TestApplicationAccessSessionRejectsWrongEndpointAndRevokedRevision(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	endpoint, _ := applicationAccessFixture(t)
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost:18608"
	origin, err := h.applicationOrigin()
	if err != nil {
		t.Fatal(err)
	}
	token, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), testWorkspaceID, testUserID, applicationTestMemberID(t, testUserID), endpoint.Revision)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://endpoint.apps.localhost/", nil)
	request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: token})
	if _, allowed := h.applicationAccessAllowed(request, origin, endpoint); !allowed {
		t.Fatal("valid application access was denied")
	}
	endpoint.Revision++
	if _, allowed := h.applicationAccessAllowed(request, origin, endpoint); allowed {
		t.Fatal("revoked endpoint revision remained authorized")
	}
	endpoint.ID = parseUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if _, allowed := h.applicationAccessAllowed(request, origin, endpoint); allowed {
		t.Fatal("one application's token accessed another application")
	}
}

func TestApplicationServiceAccessEnforcesAgentProjectScope(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	endpoint, runtimeID := applicationAccessFixture(t)
	agentID := dbfx.Agent(t, "application access agent", runtimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	dbfx.Project(t, "agent permitted project", testutil.Cols{"lead_type": "agent", "lead_id": agentID})
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost"
	request := func() *http.Request {
		r := squadScopeReq("", http.MethodPost, "/api/applications/access", nil, map[string]string{"id": uuidToString(endpoint.ApplicationID), "endpointId": uuidToString(endpoint.ID)})
		r.Header.Set("X-Actor-Source", "task_token")
		r.Header.Set("X-Agent-ID", agentID)
		r.Header.Set("X-Task-ID", taskID)
		return r
	}
	testutil.Call(t, h.CreateApplicationServiceAccess, request()).Want(http.StatusForbidden)
	if _, err := testPool.Exec(context.Background(), "UPDATE project SET lead_type='agent',lead_id=$1 WHERE id=(SELECT project_id FROM application WHERE id=$2)", agentID, uuidToString(endpoint.ApplicationID)); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	testutil.Call(t, h.CreateApplicationServiceAccess, request()).Want(http.StatusOK).JSON(&receipt)
	claims, err := applicationgateway.ParseAccess(auth.JWTSecret(), strings.TrimPrefix(receipt.Token, "mas_"))
	if err != nil || claims.EndpointID != uuidToString(endpoint.ID) || claims.Subject != testUserID {
		t.Fatalf("service access scope=%+v err=%v", claims, err)
	}
}
