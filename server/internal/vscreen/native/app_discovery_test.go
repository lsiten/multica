package native

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"strings"
	"testing"
	"time"
)

func (f *fakeAppController) ListApps(context.Context, appcontrol.Authority) (appcontrol.AppList, error) {
	return appcontrol.AppList{Apps: []appcontrol.InstalledApp{{BundleID: "org.example.Editor", Name: "Editor"}}}, nil
}

func TestAppListRequiresResumedTaskLease(t *testing.T) {
	h, a, _ := appFixture(t)
	req := appReq("app_list", a)
	if _, err := h.execute(t.Context(), req); err == nil {
		t.Fatal("listed apps without lease")
	}
	if err := h.grant(appReq("app_grant", a)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execute(t.Context(), req); err == nil {
		t.Fatal("listed apps before resume")
	}
	if _, err := h.execute(t.Context(), appReq("app_resume", a)); err != nil {
		t.Fatal(err)
	}
	out, err := h.execute(t.Context(), req)
	if err != nil || out.Apps == nil || len(out.Apps.Apps) != 1 {
		t.Fatalf("inventory=%+v err=%v", out, err)
	}
	req.App.Authority.TaskID = "forged"
	if _, err := h.execute(t.Context(), req); err == nil {
		t.Fatal("listed apps for foreign task")
	}
}

func (f *fakeAppController) ListWindows(context.Context, appcontrol.HumanRequest) (appcontrol.WindowCandidates, error) {
	return appcontrol.WindowCandidates{}, nil
}
func (f *fakeAppController) AdoptWindow(context.Context, appcontrol.HumanRequest) (appcontrol.Window, error) {
	return appcontrol.Window{}, nil
}

func TestAppHumanSelectionGrantScopeAndQuiescence(t *testing.T) {
	for _, tc := range []string{"valid", "scope", "replay", "resumed", "expiry", "window"} {
		t.Run(tc, func(t *testing.T) {
			h, a, _ := appFixture(t)
			if err := h.grant(appReq("app_grant", a)); err != nil {
				t.Fatal(err)
			}
			req := appReq("app_human_grant", a)
			req.App.Human = &HumanGrant{Capability: strings.Repeat("c", 32), InterventionID: "intervention", Direction: "list_existing"}
			if err := h.issueHuman(req); err != nil {
				t.Fatal(err)
			}
			human := appcontrol.HumanRequest{Resource: a.Resource, Grant: req.App.Human.Capability, InterventionID: "intervention", Direction: "list_existing"}
			switch tc {
			case "scope":
				human.InterventionID = "other"
			case "replay":
				if _, err := h.authorizeHuman(t.Context(), human); err != nil {
					t.Fatal(err)
				}
			case "resumed":
				h.leases[a.Resource].quiescent = false
				h.leases[a.Resource].ready = true
			case "expiry":
				h.leases[a.Resource].humanExpiry = time.Now().Add(-time.Second)
			case "window":
				human.WindowHandle = "forged"
			}
			d, err := h.authorizeHuman(t.Context(), human)
			if tc == "valid" {
				if err != nil || !d.Virtual {
					t.Fatalf("display=%+v err=%v", d, err)
				}
			} else if err == nil {
				t.Fatal("invalid local owner grant accepted")
			}
		})
	}
}
