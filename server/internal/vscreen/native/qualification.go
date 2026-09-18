package native

import (
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"os"
	"reflect"
	"sync"
	"time"
)

type inputQualification struct {
	before      smokefixture.QualificationState
	pending     map[string]protocol.VscreenActionRequest
	mu          sync.Mutex
	scope       smokefixture.QualificationScope
	window      appcontrol.Window
	process     appcontrol.Process
	codeHash    string
	inputSource string
	expires     time.Time
	validate    func() error
	source      func() string
}

// RunInputQualificationHost has a separate inherited descriptor and can authorize
// only the invocation's exact disposable fixture. RunHost never reads that descriptor.
func RunInputQualificationHost(build string) error {
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return ErrProtocol
	}
	return runHost(build, true)
}
func readInputQualification() (*inputQualification, error) {
	file := os.NewFile(7, "qualification-scope")
	if file == nil {
		return nil, ErrProtocol
	}
	defer file.Close()
	scope, err := readQualificationDescriptor(file, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if scope.InteractiveScratch {
		return nil, ErrProtocol
	}
	helper, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err = scope.Validate(os.Getppid(), helper); err != nil {
		return nil, ErrProtocol
	}
	q := &inputQualification{expires: time.Now().Add(time.Until(time.UnixMilli(scope.ExpiresAt))), inputSource: smokefixture.QualificationInputSource(), codeHash: smokefixture.QualificationCodeHash(helper), scope: scope, source: smokefixture.QualificationInputSource}
	if q.codeHash == "" || q.inputSource == "" {
		return nil, ErrProtocol
	}
	if scope.ConsumeQualificationHost() != nil {
		return nil, ErrProtocol
	}
	q.validate = func() error { return scope.Validate(os.Getppid(), helper) }
	return q, nil
}
func (q *inputQualification) check(r Request) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r.Resource != q.scope.Resource || r.App == nil {
		return appRefusal("stale_authority")
	}
	switch r.Operation {
	case "app_cancel", "app_revoke", "app_quiesce", "app_dispose":
		return nil
	}
	if !time.Now().Before(q.expires) || q.validate() != nil {
		return appRefusal("stale_authority")
	}
	switch r.Operation {
	case "app_grant", "app_renew", "app_resume":
		return nil
	case "app_launch":
		if q.window.Handle != "" || r.App.Launch == nil || r.App.Launch.BundleID != q.scope.BundleID || len(r.App.Launch.Files) != 0 {
			return appRefusal("needs_intervention")
		}
		return nil
	case "app_observe":
		if q.window.Handle != "" && r.App.WindowHandle == q.window.Handle {
			return nil
		}
	case "app_action":
		if q.window.Handle != "" && r.App.Action != nil && r.App.Action.Target.WindowHandle == q.window.Handle && qualificationAction(r.App.Action.Action) {
			if q.pending == nil {
				q.pending = make(map[string]protocol.VscreenActionRequest)
			}
			action := *r.App.Action
			if prior, ok := q.pending[action.ActionID]; ok && !reflect.DeepEqual(prior, action) {
				return appRefusal("action_conflict")
			}
			if len(q.pending) >= 16 {
				return appRefusal("app_limit")
			}
			q.pending[action.ActionID] = action
			return nil
		}
	}
	return appRefusal("needs_intervention")
}
func (q *inputQualification) bind(w appcontrol.Window) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.window.Handle != "" || w.Handle == "" || w.Process.BundleID != q.scope.BundleID || w.Process.UID != q.scope.Resource.UID {
		return appRefusal("stale_window")
	}
	q.window = w
	return nil
}
func (q *inputQualification) decide(p appcontrol.Process, a protocol.VscreenAction) appcontrol.PIDInputDecision {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.window.Handle == "" || p.PID != q.window.Process.PID || p.Start != q.window.Process.Start || p.UID != q.scope.Resource.UID || p.BundleID != q.scope.BundleID || p.ExecutablePath != q.scope.Executable() || p.CodeHash == "" || p.CodeHash != q.codeHash || p.SigningID == "" || p.OSBuild == "" {
		return appcontrol.PIDInputDecision{}
	}
	if !time.Now().Before(q.expires) || q.validate() != nil {
		return appcontrol.PIDInputDecision{}
	}
	state, err := q.scope.Read()
	if err != nil || state.PID != p.PID || state.ProcessStart != p.Start || state.WindowID != q.window.WindowID || state.DisplayID != q.window.DisplayID || state.Closed {
		return appcontrol.PIDInputDecision{}
	}
	if q.process.PID == 0 {
		q.process = p
	}
	if q.process != p {
		return appcontrol.PIDInputDecision{}
	}
	if !qualificationAction(a) {
		return appcontrol.PIDInputDecision{}
	}
	source := q.source()
	if source == "" || source != q.inputSource {
		return appcontrol.PIDInputDecision{}
	}
	q.before = state
	return appcontrol.PIDInputDecision{Certified: true, InputSourceID: source}
}
func qualificationAction(a protocol.VscreenAction) bool {
	if a.Validate() != nil {
		return false
	}
	switch a.Kind {
	case "click":
		return a.Click != nil && a.Click.ElementHandle == ""
	case "type":
		return a.Type != nil && a.Type.Text == "PID 中文 e\u0301Z"
	case "key":
		return a.Key != nil && a.Key.Key == "Backspace" && len(a.Key.Modifiers) == 0
	case "scroll":
		return a.Scroll != nil && a.Scroll.DeltaX == 0 && a.Scroll.DeltaY == -120
	case "drag":
		return a.Drag != nil && a.Drag.DurationMS == 250
	}
	return false
}

func readQualificationDescriptor(file *os.File, timeout time.Duration) (smokefixture.QualificationScope, error) {
	var scope smokefixture.QualificationScope
	stat, err := file.Stat()
	if err != nil || stat.Mode()&os.ModeNamedPipe == 0 {
		return scope, ErrProtocol
	}
	reader, err := qualificationPollablePipe(file)
	if err != nil {
		return scope, ErrProtocol
	}
	defer reader.Close()
	if reader.SetReadDeadline(time.Now().Add(timeout)) != nil {
		return scope, ErrProtocol
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &scope) != nil {
		return scope, ErrProtocol
	}
	return scope, nil
}
func (q *inputQualification) checkControl(r Request) error {
	switch r.Operation {
	case "list":
		return nil
	case "ensure", "describe", "quiesce", "dispose", "sources":
	default:
		return ErrProtocol
	}
	if r.Resource != q.scope.Resource {
		return ErrProtocol
	}
	if r.Operation == "ensure" && (r.Width != 1280 || r.Height != 720) {
		return ErrProtocol
	}
	return nil
}
