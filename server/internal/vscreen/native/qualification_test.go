package native

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func qualificationFixture(t *testing.T) (*inputQualification, appcontrol.Process, protocol.VscreenAction) {
	t.Helper()
	key := protocol.ResourceKey{BackendIdentity: "https://qualification.invalid", WorkspaceID: "ws", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	scope := smokefixture.QualificationScope{Resource: key, Directory: t.TempDir(), Nonce: strings.Repeat("a", 32), BundleID: "ai.multica.smoke." + strings.Repeat("a", 32)}
	p := appcontrol.Process{PID: 123, UID: key.UID, Start: "incarnation", BundleID: scope.BundleID, OSBuild: "fake-os", ExecutablePath: scope.Executable(), SigningID: "signed-helper", CodeHash: strings.Repeat("b", 40)}
	q := &inputQualification{expires: time.Now().Add(time.Minute), scope: scope, window: appcontrol.Window{Handle: "own", WindowID: 8, DisplayID: 42, Process: p}, codeHash: p.CodeHash, inputSource: "fake-layout", source: func() string { return "fake-layout" }, validate: func() error { return nil }}
	raw, _ := json.Marshal(smokefixture.QualificationState{State: smokefixture.State{Nonce: scope.Nonce, PID: p.PID, ProcessStart: p.Start, WindowID: 8, DisplayID: 42}})
	if err := os.WriteFile(filepath.Join(scope.Directory, "readback.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return q, p, protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "text", Text: "PID 中文 e\u0301Z"}}
}
func TestQualificationBindsDynamicCodeIdentityAndAction(t *testing.T) {
	for _, name := range []string{"valid", "pid", "start", "path", "dynamic_hash", "unsigned", "bundle", "uid", "os", "owner", "layout", "text", "window", "display", "expired"} {
		t.Run(name, func(t *testing.T) {
			q, p, a := qualificationFixture(t)
			switch name {
			case "pid":
				p.PID++
			case "start":
				p.Start = "reused"
			case "path":
				p.ExecutablePath = "/other"
			case "dynamic_hash":
				p.CodeHash = strings.Repeat("c", 40)
			case "unsigned":
				p.SigningID = ""
			case "bundle":
				p.BundleID = "other"
			case "uid":
				p.UID++
			case "os":
				p.OSBuild = ""
			case "owner":
				q.validate = func() error { return errors.New("owner_gone") }
			case "layout":
				q.source = func() string { return "changed" }
			case "text":
				a.Type.Text = "arbitrary"
			case "window":
				q.window.WindowID++
			case "expired":
				q.expires = time.Now().Add(-time.Second)
			case "display":
				q.window.DisplayID++
			}
			decision := q.decide(p, a)
			if decision.Certified != (name == "valid") {
				t.Fatalf("%s decision=%+v", name, decision)
			}
			if appcontrol.ProductionPIDInputPolicy().Decide(p, a).Certified || appcontrol.ProductionPIDInputPolicy().Verification(p) != "none" {
				t.Fatal("experiment changed production policy")
			}
		})
	}
}
func TestQualificationRejectsScopeWindowAndHumanPaths(t *testing.T) {
	q, _, _ := qualificationFixture(t)
	a := appcontrol.Authority{Resource: q.scope.Resource}
	for _, op := range []string{"app_human_grant", "app_human_transfer", "app_human_candidates", "app_human_adopt", "app_list", "app_probe", "app_managed_windows"} {
		if q.check(appReq(op, a)) == nil {
			t.Fatal("accepted", op)
		}
	}
	r := appReq("app_observe", a)
	r.App.WindowHandle = q.window.Handle
	if q.check(r) != nil {
		t.Fatal("own observation refused")
	}
	r.App.WindowHandle = "foreign"
	if q.check(r) == nil {
		t.Fatal("foreign window accepted")
	}
	r = appReq("app_grant", a)
	r.Resource.RuntimeID = "other"
	if q.check(r) == nil {
		t.Fatal("cross resource accepted")
	}
}
func TestQualificationScopeDoesNotBypassHostLeaseFence(t *testing.T) {
	h, a, _ := appFixture(t)
	q, _, _ := qualificationFixture(t)
	q.scope.Resource = a.Resource
	h.qualification = q
	if err := h.grant(appReq("app_grant", a)); err != nil {
		t.Fatal(err)
	}
	r := appReq("app_observe", a)
	r.App.WindowHandle = q.window.Handle
	if _, err := h.execute(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.fenceLocked(a.Resource)
	h.mu.Unlock()
	if _, err := h.execute(t.Context(), r); err == nil {
		t.Fatal("old lease authorized after revoke")
	}
	a.LeaseEpoch++
	r = appReq("app_action", a)
	if _, err := h.execute(t.Context(), r); err == nil {
		t.Fatal("ungranted generation accepted")
	}
}
