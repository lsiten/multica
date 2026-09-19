//go:build darwin || linux

package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"sync"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func startVscreenTestAppHost(token []byte, media net.Conn, mediaMu *sync.Mutex) (func(), error) {
	f := os.NewFile(6, "apps")
	app, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	given := make([]byte, 32)
	if _, err = io.ReadFull(app, given); err != nil || !bytes.Equal(token, given) {
		app.Close()
		return nil, native.ErrProtocol
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		leases := map[protocol.ResourceKey]appcontrol.Authority{}
		observers := map[string]appcontrol.Authority{}
		revoked := map[protocol.ResourceKey]bool{}
		humans := map[string]native.HumanGrant{}
		candidates := map[string]native.HumanGrant{}
		managed := map[protocol.ResourceKey]map[string]appcontrol.ManagedWindow{}
		humanWindows := map[string]bool{}
		revision := uint64(0)
		value := ""
		readState := func() smokefixture.State {
			var state smokefixture.State
			if path := os.Getenv("VSCREEN_TAKEOVER_OWNED_READBACK"); path != "" {
				raw, _ := os.ReadFile(path)
				_ = json.Unmarshal(raw, &state)
			}
			return state
		}
		writeState := func(state smokefixture.State) {
			if path := os.Getenv("VSCREEN_TAKEOVER_OWNED_READBACK"); path != "" {
				raw, _ := json.Marshal(state)
				_ = os.WriteFile(path+".next", raw, 0600)
				_ = os.Rename(path+".next", path)
			}
		}

		for {
			var r native.Request
			if native.ReadMessage(app, &r) != nil {
				return
			}
			reply := native.Response{Version: 1, Build: "fixture/commit", ID: r.ID, Epoch: r.Epoch, App: &native.AppResponse{}}
			if r.App == nil {
				return
			}
			a := r.App.Authority
			switch r.Operation {
			case "app_probe":
				reply.App.Permissions = &appcontrol.Permissions{}
			case "app_request_permissions":
				if path := os.Getenv("VSCREEN_FIXTURE_PERMISSION_REQUEST_COUNT"); path != "" {
					if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
						_, _ = f.WriteString("x")
						_ = f.Close()
					}
				}
				permissions := appcontrol.Permissions{Accessibility: r.App.Permissions.Accessibility, ScreenRecording: r.App.Permissions.ScreenRecording}
				if os.Getenv("VSCREEN_FIXTURE_PERMISSION_REQUEST") == "screen_recording_denied" {
					permissions.ScreenRecording = false
				}
				if os.Getenv("VSCREEN_FIXTURE_PERMISSION_REQUEST") == "accessibility_denied" {
					permissions.Accessibility = false
				}
				reply.App.Permissions = &permissions
			case "app_grant":
				if a.TaskID == "" || a.TransactionID == "" || a.LeaseEpoch == 0 {
					reply.Error = "lease_expired"
				} else {
					leases[a.Resource] = a
					revoked[a.Resource] = false
				}
			case "app_renew", "app_resume":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "lease_expired"
				}
			case "app_revoke", "app_quiesce":
				revoked[a.Resource] = true
			case "app_observer_grant":
				if a.TaskID != "" || a.TransactionID != "" || a.LeaseEpoch != 0 || a.ObserverGrant == "" {
					reply.Error = "lease_expired"
				} else {
					observers[a.ObserverGrant] = a
				}
			case "app_observer_revoke":
				delete(observers, a.ObserverGrant)
			case "app_human_grant":
				if !revoked[a.Resource] || r.App.Human == nil {
					reply.Error = "action_uncertain"
				} else {
					humans[r.App.Human.Capability] = *r.App.Human
				}
			case "app_human_candidates":
				g := r.App.Human
				if g == nil || humans[g.Capability] != *g || g.Direction != "list_existing" || g.WindowHandle != "" || g.InterventionID == "" {
					reply.Error = "human_grant_required"
					break
				}
				delete(humans, g.Capability)
				candidates["private-candidate"] = *g
				reply.App.Candidates = &appcontrol.WindowCandidates{Windows: []appcontrol.WindowCandidate{{Handle: "private-candidate", BundleID: "org.example.Editor", Title: "Private document title"}}}
			case "app_human_adopt":
				g := r.App.Human
				if g == nil || humans[g.Capability] != *g || g.Direction != "adopt_existing" || candidates[g.WindowHandle].InterventionID != g.InterventionID {
					reply.Error = "stale_window"
					break
				}
				delete(humans, g.Capability)
				delete(candidates, g.WindowHandle)
				if os.Getenv("VSCREEN_FIXTURE_ADOPT_ERROR") != "" {
					reply.Error = "stale_window"
					break
				}
				reply.App.Window = &appcontrol.Window{Handle: g.WindowHandle}
				if managed[a.Resource] == nil {
					managed[a.Resource] = map[string]appcontrol.ManagedWindow{}
				}
				managed[a.Resource][g.WindowHandle] = appcontrol.ManagedWindow{Handle: g.WindowHandle, BundleID: "org.example.Editor"}
				humanWindows[g.WindowHandle] = true
			case "app_human_transfer":
				if r.App.Human == nil || humans[r.App.Human.Capability] != *r.App.Human {
					reply.Error = "permission_denied"
				} else {
					delete(humans, r.App.Human.Capability)
					reply.App.Window = &appcontrol.Window{Handle: r.App.Human.WindowHandle}
					humanWindows[r.App.Human.WindowHandle] = r.App.Human.Direction == "to_real"
					state := readState()
					state.DisplayID = 2
					if r.App.Human.Direction == "to_real" {
						state.DisplayID = 1
					}
					writeState(state)

				}
			case "app_managed_windows":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "stale_authority"
					break
				}
				windows := []appcontrol.ManagedWindow{}
				for _, window := range managed[a.Resource] {
					if !humanWindows[window.Handle] {
						windows = append(windows, window)
					}
				}
				reply.App.ManagedWindows = &windows
			case "app_launch":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "lease_expired"
				} else {
					reply.App.Window = &appcontrol.Window{Handle: "wire-window", Process: appcontrol.Process{BundleID: r.App.Launch.BundleID}}
					if managed[a.Resource] == nil {
						managed[a.Resource] = map[string]appcontrol.ManagedWindow{}
					}
					managed[a.Resource]["wire-window"] = appcontrol.ManagedWindow{Handle: "wire-window", BundleID: r.App.Launch.BundleID}
					writeState(smokefixture.State{PID: 123, ProcessStart: "owned-fixture-start", WindowID: 9, DisplayID: 2, Bounds: smokefixture.WindowBounds{X: 10, Y: 10, Width: 640, Height: 440}})

				}
			case "app_action":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "lease_expired"
				} else {
					outcome := protocol.VscreenActionVerified
					if r.App.Action.Action.Type != nil {
						value = r.App.Action.Action.Type.Text
						state := readState()
						state.Text = value
						writeState(state)
					} else {
						outcome = protocol.VscreenActionUncertain
					}
					reply.App.Result = &appcontrol.Result{Outcome: outcome}
				}
			case "app_observe":
				if os.Getenv("VSCREEN_FIXTURE_OBSERVE_ERROR") != "" {
					reply.Error = "screen_recording_denied"
					break
				}
				if (a.ObserverGrant == "" && (leases[a.Resource] != a || revoked[a.Resource])) || (a.ObserverGrant != "" && observers[a.ObserverGrant] != a) {
					reply.Error = "lease_expired"
					break
				}
				revision++
				if os.Getenv("VSCREEN_TAKEOVER_OWNED_READBACK") != "" {
					value = readState().Text
				}
				o := appcontrol.Observation{Display: appcontrol.Display{Resource: a.Resource, Epoch: a.Epoch, ID: 2, Virtual: true}, Window: appcontrol.Window{Handle: r.App.WindowHandle, SnapshotRevision: revision, WindowID: 9, Process: appcontrol.Process{PID: 123, Start: "owned-fixture-start"}}, Width: 2, Height: 1, Elements: []appcontrol.Element{{Handle: "entry", Title: "Multica smoke text", SetValue: true, Value: value}}}
				reply.App.Observation = &o
				if r.App.IncludePNG {
					pixels := image.NewRGBA(image.Rect(0, 0, 2, 1))
					pixels.Set(0, 0, color.RGBA{R: 255, A: 255})
					if value != "" {
						pixels.Set(1, 0, color.RGBA{G: 255, A: 255})
					}
					var encoded bytes.Buffer
					if png.Encode(&encoded, pixels) != nil {
						return
					}
					data := encoded.Bytes()
					sum := sha256.Sum256(data)
					reply.App.Snapshot = &native.SnapshotDescriptor{ID: r.App.SnapshotID, Resource: a.Resource, Epoch: a.Epoch, WindowHandle: r.App.WindowHandle, SnapshotRevision: revision, DisplayID: 2, Size: uint32(len(data)), SHA256: hex.EncodeToString(sum[:])}
					mediaMu.Lock()
					err := native.WriteMediaSample(media, native.MediaSample{Kind: native.MediaSnapshot, StreamID: r.App.SnapshotID, Epoch: a.Epoch, DisplayID: 2, SnapshotTotal: uint32(len(data)), PNG: data})
					mediaMu.Unlock()
					if err != nil {
						return
					}
				}
			}
			if native.WriteMessage(app, reply) != nil {
				return
			}
		}
	}()
	return func() { app.Close(); <-done }, nil
}
