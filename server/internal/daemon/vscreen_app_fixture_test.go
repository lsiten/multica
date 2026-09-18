//go:build darwin || linux

package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"sync"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
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
		revision := uint64(0)
		value := ""
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
			case "app_human_transfer":
				if r.App.Human == nil || humans[r.App.Human.Capability] != *r.App.Human {
					reply.Error = "permission_denied"
				} else {
					delete(humans, r.App.Human.Capability)
					reply.App.Window = &appcontrol.Window{Handle: r.App.Human.WindowHandle}
				}
			case "app_launch":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "lease_expired"
				} else {
					reply.App.Window = &appcontrol.Window{Handle: "wire-window"}
				}
			case "app_action":
				if leases[a.Resource] != a || revoked[a.Resource] {
					reply.Error = "lease_expired"
				} else {
					outcome := protocol.VscreenActionVerified
					if r.App.Action.Action.Type != nil {
						value = r.App.Action.Action.Type.Text
					} else {
						outcome = protocol.VscreenActionUncertain
					}
					reply.App.Result = &appcontrol.Result{Outcome: outcome}
				}
			case "app_observe":
				if (a.ObserverGrant == "" && (leases[a.Resource] != a || revoked[a.Resource])) || (a.ObserverGrant != "" && observers[a.ObserverGrant] != a) {
					reply.Error = "lease_expired"
					break
				}
				revision++
				o := appcontrol.Observation{Display: appcontrol.Display{Resource: a.Resource, Epoch: a.Epoch, ID: 2, Virtual: true}, Window: appcontrol.Window{Handle: r.App.WindowHandle, SnapshotRevision: revision}, Width: 2, Height: 1, Elements: []appcontrol.Element{{Handle: "entry", Value: value}}}
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
