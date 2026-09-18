package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type desktopVscreenCredential struct {
	Capability  string `json:"capability"`
	Incarnation string `json:"incarnation"`
	PID         int    `json:"pid"`
	Profile     string `json:"profile"`
	DaemonID    string `json:"daemon_id"`
	Backend     string `json:"backend"`
	StartedAt   string `json:"started_at"`
}

func writeDesktopVscreenCredential(directory string, c desktopVscreenCredential) (func(), error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !desktopFileOwned(info) {
		return nil, errors.New("private profile directory required")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return nil, errors.New("profile symlink rejected")
	}
	directory = filepath.Join(directory, ".desktop-vscreen")
	if err = os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err = os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !desktopFileOwned(info) {
		return nil, errors.New("private directory required")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, ".vscreen-credential-")
	if err != nil {
		return nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	target := filepath.Join(directory, "credential.json")
	if err = os.Rename(name, target); err != nil {
		return nil, err
	}
	return func() {
		current, e := os.ReadFile(target)
		if e == nil && subtle.ConstantTimeCompare(current, raw) == 1 {
			os.Remove(target)
		}
	}, nil
}
func (d *Daemon) registerVscreenDesktop(ctx context.Context, mux *http.ServeMux, startedAt time.Time) func() {
	if d.cfg.LaunchedBy != "desktop" || !strings.HasPrefix(d.cfg.Profile, "desktop-") {
		return func() {}
	}
	directory, err := cli.ProfileDir(d.cfg.Profile)
	if err != nil {
		return func() {}
	}
	var token [32]byte
	var incarnation [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return func() {}
	}
	if _, err = rand.Read(incarnation[:]); err != nil {
		return func() {}
	}
	credential := desktopVscreenCredential{Capability: hex.EncodeToString(token[:]), Incarnation: hex.EncodeToString(incarnation[:]), PID: os.Getpid(), Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Backend: strings.TrimRight(d.cfg.ServerBaseURL, "/"), StartedAt: startedAt.UTC().Format(time.RFC3339Nano)}
	cleanup, err := writeDesktopVscreenCredential(directory, credential)
	if err != nil {
		return func() {}
	}
	verify := func(call context.Context, capability string) bool {
		return ctx.Err() == nil && call.Err() == nil && subtle.ConstantTimeCompare([]byte(capability), []byte(credential.Capability)) == 1
	}
	d.SetVscreenLocalOwnerVerifier(verify)
	mux.Handle("/vscreen/desktop", d.vscreenDesktopHandler(credential, verify))
	return func() { d.SetVscreenLocalOwnerVerifier(nil); cleanup() }
}
func (d *Daemon) vscreenDesktopHandler(c desktopVscreenCredential, verify func(context.Context, string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var candidates *appcontrol.WindowCandidates
		var selectionRequired bool
		var interventionID string
		reply := func(status int, reason string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			value := map[string]any{"ok": status == 200, "local": true, "reason": reason}
			if interventionID != "" {
				value["intervention_id"] = interventionID
				value["selection_required"] = selectionRequired
			}
			if status == 200 && candidates != nil {
				value["candidates"] = candidates
			}
			json.NewEncoder(w).Encode(value)
		}
		if r.Method != "POST" {
			reply(405, "method_not_allowed")
			return
		}
		capability := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Header.Get("Origin") != "" || r.Header.Get("X-Multica-Profile") != c.Profile || r.Header.Get("X-Vscreen-Incarnation") != c.Incarnation || !verify(r.Context(), capability) {
			reply(403, "local_owner_required")
			return
		}
		var body struct {
			Action         string   `json:"action"`
			WorkspaceID    string   `json:"workspace_id"`
			RuntimeID      string   `json:"runtime_id"`
			InterventionID string   `json:"intervention_id"`
			Destination    string   `json:"destination_source_id"`
			Summary        string   `json:"summary"`
			WindowHandle   string   `json:"window_handle"`
			Excluded       []uint32 `json:"excluded_window_ids"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
			reply(400, "invalid_request")
			return
		}
		operation, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if body.Action == "exclusions" {
			if len(body.Excluded) > 32 {
				reply(400, "invalid_request")
				return
			}
			if err := d.updateVscreenExclusions(operation, body.Excluded); err != nil {
				reply(409, "capture_update_failed")
				return
			}
			reply(200, "")
			return
		}
		if _, err := d.vscreenResource(body.WorkspaceID, body.RuntimeID); err != nil {
			reply(403, "runtime_not_local")
			return
		}
		var err error
		switch body.Action {
		case "status":
			s := d.vscreenRuntime()
			s.interventions.mu.Lock()
			record := s.interventions.records[body.RuntimeID]
			var id string
			var state protocol.VscreenInterventionState
			if record != nil {
				id = record.Report.InterventionID
				state = record.Report.State
				interventionID = record.Report.InterventionID
				selectionRequired = record.Stopped && state == protocol.VscreenInterventionAwaitingTakeover && len(record.Windows) == 0
			}
			s.interventions.mu.Unlock()
			d.vscreenMu.Lock()
			reporter := d.vscreenReporter
			d.vscreenMu.Unlock()
			if id != "" && (state == protocol.VscreenInterventionAwaitingTakeover || state == protocol.VscreenInterventionHuman || state == protocol.VscreenInterventionReadyToContinue) && (reporter == nil || !reporter.Acknowledged(id, state)) {
				reply(409, "report_pending")
				return
			}
		case "list_windows", "adopt_window":
			candidates, err = d.selectVscreenWindow(operation, capability, body.WorkspaceID, body.RuntimeID, body.InterventionID, body.WindowHandle, body.Action == "adopt_window")
			if err != nil {
				reply(409, selectionReason(err))
				return
			}
		case "takeover":
			err = d.TakeOverVscreenLocally(operation, capability, body.WorkspaceID, body.RuntimeID, body.InterventionID, body.Destination)
		case "return":
			err = d.ReturnVscreenLocally(operation, capability, body.WorkspaceID, body.RuntimeID, body.InterventionID, body.Summary)
		default:
			reply(400, "invalid_request")
			return
		}
		if err != nil {
			reason := "handoff_failed"
			if err.Error() == "report_pending" {
				reason = "report_pending"
			}
			reply(409, reason)
			return
		}
		reply(200, "")
	})
}
func (d *Daemon) updateVscreenExclusions(ctx context.Context, ids []uint32) error {
	if len(ids) > 32 {
		return errors.New("invalid exclusions")
	}
	seen := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			return errors.New("invalid exclusions")
		}
		seen[id] = true
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.excludedWindows = append([]uint32(nil), ids...)
	client := s.client
	if client == nil {
		return nil
	}
	return client.UpdateExclusions(ctx, ids)
}
