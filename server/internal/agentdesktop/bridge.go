package agentdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	BridgeCommand = "internal-agent-desktop-bridge"
	ServerName    = "agent-desktop"
)

type BridgeConfig struct {
	MulticaExecutable string
	TaskID            string
	RuntimeID         string
	Executable        string
	Headed            bool
}

type bridgeEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

type sessionData struct {
	SessionID string `json:"session_id"`
}

// MCPConfig returns a provider-native stdio MCP entry. The bridge is a control
// plane around agent-desktop; it does not implement desktop actions.
func MCPConfig(config BridgeConfig) (json.RawMessage, error) {
	if !filepathAbsolute(config.MulticaExecutable) {
		return nil, errors.New("multica executable must be an absolute path")
	}
	if strings.TrimSpace(config.TaskID) == "" || strings.TrimSpace(config.RuntimeID) == "" {
		return nil, errors.New("task and runtime identity are required")
	}
	args := []string{
		BridgeCommand,
		"--task-id", config.TaskID,
		"--runtime-id", config.RuntimeID,
		"--agent-desktop", config.Executable,
	}
	if config.Headed {
		args = append(args, "--headed")
	}
	return json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			ServerName: map[string]any{
				"command": config.MulticaExecutable,
				"args":    args,
			},
		},
	})
}

// RunBridge owns one task-scoped session and forwards the agent-desktop MCP
// protocol unchanged. It performs cleanup only after the child has exited.
func RunBridge(ctx context.Context, config BridgeConfig, stdin io.Reader, stdout, stderr io.Writer) error {
	if strings.TrimSpace(config.Executable) == "" {
		return errors.New("agent-desktop executable is required")
	}
	if err := Prepare(ctx, config.Executable); err != nil {
		return err
	}
	sessionID, err := startSession(ctx, config, stderr)
	if err != nil {
		return err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"mcp", "--session", sessionID}
	if config.Headed {
		args = append(args, "--headed")
	}
	command := exec.CommandContext(childCtx, config.Executable, args...)
	command.Stderr = stderr
	command.Env = bridgeEnvironment(os.Environ())
	childInput, err := command.StdinPipe()
	if err != nil {
		endErr := endSession(context.WithoutCancel(ctx), config.Executable, sessionID, stderr)
		return errors.Join(fmt.Errorf("open agent-desktop stdin: %w", err), endErr)
	}
	childOutput, err := command.StdoutPipe()
	if err != nil {
		endErr := endSession(context.WithoutCancel(ctx), config.Executable, sessionID, stderr)
		return errors.Join(fmt.Errorf("open agent-desktop stdout: %w", err), endErr)
	}
	if err := command.Start(); err != nil {
		endErr := endSession(context.WithoutCancel(ctx), config.Executable, sessionID, stderr)
		return errors.Join(fmt.Errorf("start agent-desktop MCP: %w", err), endErr)
	}
	output := &synchronizedWriter{w: stdout}
	outputErr := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(output, childOutput)
		outputErr <- copyErr
	}()
	inputErr := make(chan error, 1)
	go func() {
		inputErr <- forwardClientMessages(
			ctx,
			stdin,
			output,
			childInput,
			newHumanRequestApprover(config.TaskID, config.Headed),
			config.Headed,
		)
	}()

	var runErr error
	select {
	case runErr = <-inputErr:
		_ = childInput.Close()
	case <-ctx.Done():
		_ = childInput.Close()
		runErr = ctx.Err()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}
	waitErr := command.Wait()
	if copyErr := <-outputErr; copyErr != nil && runErr == nil {
		runErr = copyErr
	}
	endErr := endSession(context.WithoutCancel(ctx), config.Executable, sessionID, stderr)
	return errors.Join(runErr, waitErr, endErr)
}

func startSession(ctx context.Context, config BridgeConfig, stderr io.Writer) (string, error) {
	raw, err := runEnvelope(ctx, config.Executable, []string{
		"session", "start",
		"--no-trace",
		"--name", "multica-" + shortTaskName(config.TaskID),
	}, stderr)
	if err != nil {
		return "", fmt.Errorf("start agent-desktop session: %w", err)
	}
	var data sessionData
	if err := json.Unmarshal(raw, &data); err != nil || strings.TrimSpace(data.SessionID) == "" {
		return "", errors.New("start agent-desktop session: missing session_id")
	}
	return data.SessionID, nil
}

func endSession(ctx context.Context, executable, sessionID string, stderr io.Writer) error {
	endCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := runEnvelope(endCtx, executable, []string{"session", "end", sessionID}, stderr)
	if err != nil {
		return fmt.Errorf("end agent-desktop session: %w", err)
	}
	return nil
}

func runEnvelope(ctx context.Context, executable string, args []string, stderr io.Writer) (json.RawMessage, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = bridgeEnvironment(os.Environ())
	command.Stderr = stderr
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	var envelope bridgeEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK {
		return nil, errors.New("agent-desktop command failed")
	}
	return envelope.Data, nil
}

func shortTaskName(taskID string) string {
	const max = 80
	taskID = strings.TrimSpace(taskID)
	if len(taskID) <= max {
		return taskID
	}
	return taskID[:max]
}

func filepathAbsolute(path string) bool {
	return filepath.IsAbs(strings.TrimSpace(path))
}

// sessionCleanup lets the CLI keep a single cancellation path while the child
// and session helper finish. It intentionally does not retry an uncertain end.
func sessionCleanup(cancel context.CancelFunc, closeStdin func() error) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = closeStdin()
			cancel()
		})
	}
}
