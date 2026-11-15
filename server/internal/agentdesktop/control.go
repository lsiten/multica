package agentdesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var ErrControl = errors.New("agent-desktop control failed")

// ControlSession serializes commands for one approved human-control lease.
// Every desktop action is still executed by agent-desktop.
type ControlSession struct {
	executable string
	sessionID  string
	mu         sync.Mutex
}

type controlEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

func StartControlSession(ctx context.Context, executable, name string) (*ControlSession, error) {
	if executable == "" || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: identity is required", ErrControl)
	}
	raw, err := runEnvelope(ctx, executable, []string{
		"session", "start", "--no-trace", "--name", name,
	}, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("%w: start session: %v", ErrControl, err)
	}
	var data sessionData
	if err := json.Unmarshal(raw, &data); err != nil || strings.TrimSpace(data.SessionID) == "" ||
		strings.ContainsAny(data.SessionID, "\x00\r\n") || len(data.SessionID) > 128 {
		return nil, fmt.Errorf("%w: invalid session response", ErrControl)
	}
	return &ControlSession{executable: executable, sessionID: data.SessionID}, nil
}

func (s *ControlSession) Execute(ctx context.Context, args []string, input []byte) (json.RawMessage, error) {
	if s == nil || s.executable == "" || s.sessionID == "" || len(args) == 0 {
		return nil, fmt.Errorf("%w: session unavailable", ErrControl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	commandArgs := append(append([]string(nil), args...), "--session", s.sessionID, "--headed")
	return runControlEnvelope(ctx, s.executable, commandArgs, input)
}

func (s *ControlSession) Close(ctx context.Context) error {
	if s == nil || s.executable == "" || s.sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := runControlEnvelope(ctx, s.executable, []string{"session", "end", s.sessionID}, nil)
	if err != nil {
		return fmt.Errorf("%w: end session", ErrControl)
	}
	return nil
}

func runControlEnvelope(
	ctx context.Context,
	executable string,
	args []string,
	input []byte,
) (json.RawMessage, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = bridgeEnvironment(os.Environ())
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%w: command exited", ErrControl)
		}
		return nil, fmt.Errorf("%w: command unavailable", ErrControl)
	}
	var envelope controlEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("%w: invalid response", ErrControl)
	}
	if !envelope.OK {
		return nil, fmt.Errorf("%w: rejected", ErrControl)
	}
	return envelope.Data, nil
}
