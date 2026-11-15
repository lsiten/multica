package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentDesktopControlSession struct {
	id          string
	workspaceID string
	runtimeID   string
	lease       *agentDesktopExecution
	session     *agentdesktop.ControlSession
	mu          sync.Mutex
	lastX       float64
	lastY       float64
	hasLast     bool
}

func (d *Daemon) handleAgentDesktopControlStartQuery(
	ctx context.Context,
	query protocol.AgentDesktopControlStartQuery,
	enqueue func([]byte) (*wsOutbound, error),
	generation mirrorConnectionGeneration,
) {
	result := protocol.AgentDesktopControlResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		ControlID:       query.ControlID,
	}
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	control, err := d.startAgentDesktopControl(ctx, query)
	if err != nil {
		result.State = "failed"
		result.Reason = agentDesktopControlFailure(err)
	} else {
		result.State = "started"
		d.logger.Info("agent-desktop human control started",
			"runtime_id", control.runtimeID,
			"control_id", control.id,
		)
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventAgentDesktopControlStartResult, result); err != nil {
		d.logger.Debug("agent-desktop control start result dropped", "error", err)
	}
}

func (d *Daemon) handleAgentDesktopControlInputQuery(
	ctx context.Context,
	query protocol.AgentDesktopControlInputQuery,
	enqueue func([]byte) (*wsOutbound, error),
	generation mirrorConnectionGeneration,
) {
	result := protocol.AgentDesktopControlResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		ControlID:       query.ControlID,
		State:           "ok",
	}
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	control := d.agentDesktopControl(query.ControlID, query.RuntimeID)
	if control == nil {
		result.State = "failed"
		result.Reason = "control_unavailable"
	} else {
		clipboard, err := d.executeAgentDesktopControl(ctx, control, query.Input)
		if err != nil {
			result.State = "failed"
			result.Reason = "control_command_failed"
		}
		result.Clipboard = clipboard
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventAgentDesktopControlInputResult, result); err != nil {
		d.logger.Debug("agent-desktop control input result dropped", "error", err)
	}
}

func (d *Daemon) handleAgentDesktopControlStopQuery(
	ctx context.Context,
	query protocol.AgentDesktopControlStopQuery,
	enqueue func([]byte) (*wsOutbound, error),
	generation mirrorConnectionGeneration,
) {
	result := protocol.AgentDesktopControlResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		ControlID:       query.ControlID,
		State:           "ended",
	}
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	if err := d.stopAgentDesktopControl(query.RuntimeID, query.ControlID); err != nil {
		result.State = "failed"
		result.Reason = "control_stop_failed"
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventAgentDesktopControlStopResult, result); err != nil {
		d.logger.Debug("agent-desktop control stop result dropped", "error", err)
	}
}

func (d *Daemon) startAgentDesktopControl(ctx context.Context, query protocol.AgentDesktopControlStartQuery) (*agentDesktopControlSession, error) {
	executable, err := agentdesktop.Resolve()
	if err != nil {
		return nil, err
	}
	if err := agentdesktop.Prepare(ctx, executable); err != nil {
		return nil, err
	}
	owner := "mirror:" + query.ControlID
	lease, err := d.acquireAgentDesktopLease(query.RuntimeID, owner)
	if err != nil {
		return nil, err
	}
	session, err := agentdesktop.StartControlSession(ctx, executable, query.Name)
	if err != nil {
		lease.Close()
		return nil, err
	}
	control := &agentDesktopControlSession{
		id:          query.ControlID,
		workspaceID: query.WorkspaceID,
		runtimeID:   query.RuntimeID,
		lease:       lease,
		session:     session,
	}
	d.agentDesktopControlMu.Lock()
	if d.agentDesktopControls == nil {
		d.agentDesktopControls = make(map[string]*agentDesktopControlSession)
	}
	d.agentDesktopControls[query.ControlID] = control
	d.agentDesktopControlMu.Unlock()
	return control, nil
}

func (d *Daemon) agentDesktopControl(controlID, runtimeID string) *agentDesktopControlSession {
	d.agentDesktopControlMu.Lock()
	defer d.agentDesktopControlMu.Unlock()
	control := d.agentDesktopControls[controlID]
	if control == nil || control.runtimeID != runtimeID {
		return nil
	}
	return control
}

func (d *Daemon) stopAgentDesktopControl(runtimeID, controlID string) error {
	d.agentDesktopControlMu.Lock()
	control := d.agentDesktopControls[controlID]
	if control == nil || control.runtimeID != runtimeID {
		d.agentDesktopControlMu.Unlock()
		return nil
	}
	delete(d.agentDesktopControls, controlID)
	d.agentDesktopControlMu.Unlock()
	err := control.session.Close(context.WithoutCancel(context.Background()))
	control.lease.Close()
	if errors.Is(err, agentdesktop.ErrControl) {
		return err
	}
	return nil
}

func (d *Daemon) closeAgentDesktopControls() {
	d.agentDesktopControlMu.Lock()
	controls := d.agentDesktopControls
	d.agentDesktopControls = nil
	d.agentDesktopControlMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, control := range controls {
		if err := control.session.Close(ctx); err != nil {
			d.logger.Debug("agent-desktop control cleanup failed",
				"runtime_id", control.runtimeID,
				"control_id", control.id,
			)
		}
		control.lease.Close()
	}
}

func (d *Daemon) executeAgentDesktopControl(
	ctx context.Context,
	control *agentDesktopControlSession,
	input protocol.AgentDesktopControlInput,
) (*string, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	args, stdin, err := d.agentDesktopControlArgs(ctx, control, input)
	if err != nil {
		return nil, err
	}
	data, err := control.session.Execute(ctx, args, stdin)
	if err != nil {
		return nil, err
	}
	if input.Action != protocol.AgentDesktopControlClipboardGet {
		return nil, nil
	}
	var clipboard struct {
		Found bool   `json:"found"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(data, &clipboard); err != nil {
		return nil, err
	}
	if !clipboard.Found {
		empty := ""
		return &empty, nil
	}
	return &clipboard.Text, nil
}

func (d *Daemon) agentDesktopControlArgs(
	ctx context.Context,
	control *agentDesktopControlSession,
	input protocol.AgentDesktopControlInput,
) ([]string, []byte, error) {
	needsPoint := input.Action != protocol.AgentDesktopControlPress &&
		input.Action != protocol.AgentDesktopControlText &&
		input.Action != protocol.AgentDesktopControlClipboardGet &&
		input.Action != protocol.AgentDesktopControlClipboardSet
	var source nativecapture.SourceDescriptor
	var x, y int
	if needsPoint {
		var err error
		source, err = d.agentDesktopControlSource(ctx, control.workspaceID, control.runtimeID, input.Source)
		if err != nil {
			return nil, nil, err
		}
		x, y, err = agentDesktopPoint(source, input.X, input.Y)
		if err != nil {
			return nil, nil, err
		}
	}
	switch input.Action {
	case protocol.AgentDesktopControlMove:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-move", "--xy", xy(x, y)}, nil, nil
	case protocol.AgentDesktopControlMoveRelative:
		nextX, nextY := control.relativePoint(input.X, input.Y)
		nextAbsoluteX, nextAbsoluteY, pointErr := agentDesktopPoint(source, &nextX, &nextY)
		if pointErr != nil {
			return nil, nil, pointErr
		}
		return []string{"mouse-move", "--xy", xy(nextAbsoluteX, nextAbsoluteY)}, nil, nil
	case protocol.AgentDesktopControlClick:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-click", "--xy", xy(x, y), "--button", "left"}, nil, nil
	case protocol.AgentDesktopControlDoubleClick:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-click", "--xy", xy(x, y), "--button", "left", "--count", "2"}, nil, nil
	case protocol.AgentDesktopControlRightClick:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-click", "--xy", xy(x, y), "--button", "right"}, nil, nil
	case protocol.AgentDesktopControlMouseDown:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-down", "--xy", xy(x, y), "--button", control.button(input.Button)}, nil, nil
	case protocol.AgentDesktopControlMouseUp:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-up", "--xy", xy(x, y), "--button", control.button(input.Button)}, nil, nil
	case protocol.AgentDesktopControlScroll:
		control.rememberPoint(input.X, input.Y)
		return []string{"mouse-wheel", "--x", strconv.Itoa(x), "--y", strconv.Itoa(y),
			"--dx", strconv.Itoa(input.DX), "--dy", strconv.Itoa(input.DY)}, nil, nil
	case protocol.AgentDesktopControlDrag:
		x2, y2, dragErr := agentDesktopPoint(source, input.X2, input.Y2)
		if dragErr != nil {
			return nil, nil, dragErr
		}
		control.rememberPoint(input.X2, input.Y2)
		return []string{"drag", "--from-xy", xy(x, y), "--to-xy", xy(x2, y2),
			"--duration", "320", "--drop-delay", "150"}, nil, nil
	case protocol.AgentDesktopControlPress:
		return []string{"press", input.Key}, nil, nil
	case protocol.AgentDesktopControlText:
		return d.agentDesktopTextArgs(ctx, control, input.Text)
	case protocol.AgentDesktopControlClipboardGet:
		return []string{"clipboard-get", "--format", "text"}, nil, nil
	case protocol.AgentDesktopControlClipboardSet:
		return []string{"clipboard-set", "--stdin"}, []byte(input.Text), nil
	default:
		return nil, nil, fmt.Errorf("unsupported control action")
	}
}

func (s *agentDesktopControlSession) rememberPoint(x, y *float64) {
	if x == nil || y == nil {
		return
	}
	s.lastX = *x
	s.lastY = *y
	s.hasLast = true
}

func (s *agentDesktopControlSession) relativePoint(dx, dy *float64) (float64, float64) {
	if !s.hasLast {
		s.lastX = 0.5
		s.lastY = 0.5
	}
	s.lastX = min(1, max(0, s.lastX+(dxValue(dx))))
	s.lastY = min(1, max(0, s.lastY+(dyValue(dy))))
	s.hasLast = true
	return s.lastX, s.lastY
}

func (s *agentDesktopControlSession) button(value string) string {
	if value == "" {
		return "left"
	}
	return value
}

func dxValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func dyValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func (d *Daemon) agentDesktopControlSource(
	ctx context.Context,
	workspaceID, runtimeID string,
	selected protocol.MirrorSource,
) (nativecapture.SourceDescriptor, error) {
	sources, err := d.vscreenSources(ctx, workspaceID, runtimeID)
	if err != nil {
		return nativecapture.SourceDescriptor{}, err
	}
	for _, source := range sources {
		if source.Source == selected {
			return source, nil
		}
	}
	return nativecapture.SourceDescriptor{}, errors.New("control source unavailable")
}

func (d *Daemon) agentDesktopTextArgs(
	ctx context.Context,
	control *agentDesktopControlSession,
	text string,
) ([]string, []byte, error) {
	previous, _ := currentClipboardText(ctx, control)
	if _, err := control.session.Execute(ctx, []string{"clipboard-set", "--stdin"}, []byte(text)); err != nil {
		return nil, nil, err
	}
	paste := "cmd+v"
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		paste = "ctrl+v"
	}
	_, pasteErr := control.session.Execute(ctx, []string{"press", paste}, nil)
	if previous != nil {
		_, _ = control.session.Execute(ctx, []string{"clipboard-set", "--stdin"}, []byte(*previous))
	} else {
		_, _ = control.session.Execute(ctx, []string{"clipboard-clear"}, nil)
	}
	if pasteErr != nil {
		return nil, nil, pasteErr
	}
	return nil, nil, nil
}

func currentClipboardText(ctx context.Context, control *agentDesktopControlSession) (*string, error) {
	data, err := control.session.Execute(ctx, []string{"clipboard-get", "--format", "text"}, nil)
	if err != nil {
		return nil, err
	}
	var clipboard struct {
		Found bool   `json:"found"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(data, &clipboard); err != nil || !clipboard.Found {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	return &clipboard.Text, nil
}

func agentDesktopPoint(source nativecapture.SourceDescriptor, normalizedX, normalizedY *float64) (int, int, error) {
	if normalizedX == nil || normalizedY == nil || source.LogicalWidth <= 0 || source.LogicalHeight <= 0 {
		return 0, 0, errors.New("control coordinates unavailable")
	}
	x := float64(source.X) + *normalizedX*source.LogicalWidth
	y := float64(source.Y) + *normalizedY*source.LogicalHeight
	return int(math.Round(x)), int(math.Round(y)), nil
}

func xy(x, y int) string {
	return strconv.Itoa(x) + "," + strconv.Itoa(y)
}

func agentDesktopControlFailure(err error) string {
	switch {
	case errors.Is(err, errAgentDesktopBusy):
		return "desktop_busy"
	case errors.Is(err, agentdesktop.ErrUnavailable):
		return "agent_desktop_unavailable"
	case errors.Is(err, agentdesktop.ErrVersion):
		return "agent_desktop_version_mismatch"
	case errors.Is(err, agentdesktop.ErrControl):
		return "agent_desktop_control_failed"
	default:
		return "control_start_failed"
	}
}
