//go:build darwin || linux

package hostclient

import (
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"net"
	"testing"
	"time"
)

func TestListAppsPrivateChannelRejectsMalformedInventory(t *testing.T) {
	for _, tc := range []string{"valid", "missing", "duplicate"} {
		t.Run(tc, func(t *testing.T) {
			parent, child := net.Pipe()
			c := &Client{build: "test", epoch: "native", timeout: time.Second, apps: &appChannel{conn: parent, pending: make(map[string]chan native.Response), done: make(chan struct{})}}
			go c.readApps()
			t.Cleanup(func() { parent.Close(); child.Close(); <-c.apps.done })
			done := make(chan error, 1)
			a := appAuthority(c)
			go func() {
				var req native.Request
				if err := native.ReadMessage(child, &req); err != nil {
					done <- err
					return
				}
				if req.Operation != "app_list" || req.App.Authority != a {
					done <- native.ErrProtocol
					return
				}
				out := &native.AppResponse{}
				if tc != "missing" {
					out.Apps = &appcontrol.AppList{Apps: []appcontrol.InstalledApp{{BundleID: "org.example.Editor", Name: "Editor"}}}
				}
				if tc == "duplicate" {
					out.Apps.Apps = append(out.Apps.Apps, out.Apps.Apps[0])
				}
				done <- native.WriteMessage(child, native.Response{Version: native.ProtocolVersion, Build: "test", ID: req.ID, Epoch: a.Epoch, App: out})
			}()
			list, err := c.ListApps(t.Context(), a)
			if tc == "valid" {
				if err != nil || len(list.Apps) != 1 {
					t.Fatalf("list=%+v err=%v", list, err)
				}
			} else if err == nil {
				t.Fatal("malformed native inventory exposed")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppWindowSelectionPrivateClientContracts(t *testing.T) {
	for _, tc := range []string{"list", "malformed", "adopt", "wrong_handle"} {
		t.Run(tc, func(t *testing.T) {
			parent, child := net.Pipe()
			c := &Client{build: "test", epoch: "native", timeout: time.Second, apps: &appChannel{conn: parent, pending: make(map[string]chan native.Response), done: make(chan struct{})}}
			go c.readApps()
			t.Cleanup(func() { parent.Close(); child.Close(); <-c.apps.done })
			a := appAuthority(c)
			g := native.HumanGrant{Capability: "local-owner", InterventionID: "intervention", Direction: "list_existing"}
			list := tc == "list" || tc == "malformed"
			if !list {
				g.Direction = "adopt_existing"
				g.WindowHandle = "candidate"
			}
			done := make(chan error, 1)
			go func() {
				var req native.Request
				if err := native.ReadMessage(child, &req); err != nil {
					done <- err
					return
				}
				op := "app_human_adopt"
				if list {
					op = "app_human_candidates"
				}
				if req.Operation != op || req.App.Authority != a || req.App.Human == nil || *req.App.Human != g {
					done <- native.ErrProtocol
					return
				}
				out := &native.AppResponse{}
				if list {
					out.Candidates = &appcontrol.WindowCandidates{Windows: []appcontrol.WindowCandidate{{Handle: "candidate", BundleID: "org.example.Editor", Title: "Synthetic"}}}
					if tc == "malformed" {
						out.Candidates.Windows[0].Handle = ""
					}
				} else {
					out.Window = &appcontrol.Window{Handle: g.WindowHandle}
					if tc == "wrong_handle" {
						out.Window.Handle = "other"
					}
				}
				done <- native.WriteMessage(child, native.Response{Version: native.ProtocolVersion, Build: "test", ID: req.ID, Epoch: a.Epoch, App: out})
			}()
			var err error
			if list {
				_, err = c.ListAppWindows(t.Context(), a, g)
			} else {
				_, err = c.AdoptAppWindow(t.Context(), a, g)
			}
			wantOK := tc == "list" || tc == "adopt"
			if (err == nil) != wantOK {
				t.Fatalf("selection=%s err=%v", tc, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
