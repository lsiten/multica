package agentdesktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const maxMCPMessageBytes = 4 << 20

type rpcCall struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"params"`
}

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *synchronizedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(data)
}

func forwardClientMessages(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	childInput io.Writer,
	approver desktopApprover,
	headed bool,
) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), maxMCPMessageBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		request, ok := parseToolCall(line)
		if ok {
			requirement := approvalForToolCall(request.Params.Name, request.Params.Arguments)
			requirement.CallID = string(request.ID)
			if len(requirement.Commands) > 0 {
				if err := approver.Approve(ctx, requirement, headed); err != nil {
					if len(request.ID) == 0 {
						continue
					}
					if writeErr := writeToolError(output, request.ID, approvalFailureText(err)); writeErr != nil {
						return fmt.Errorf("write rejected desktop action: %w", writeErr)
					}
					continue
				}
			}
		}
		line = append(line, '\n')
		if _, err := childInput.Write(line); err != nil {
			return fmt.Errorf("forward MCP message: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP message: %w", err)
	}
	return nil
}

func parseToolCall(line []byte) (rpcCall, bool) {
	var request rpcCall
	if json.Unmarshal(line, &request) != nil || request.Method != "tools/call" || request.Params.Name == "" {
		return rpcCall{}, false
	}
	return request, true
}

func writeToolError(output io.Writer, id json.RawMessage, message string) error {
	response := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      json.RawMessage
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
	}
	response.Result.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: message}}
	response.Result.IsError = true
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = output.Write(data)
	return err
}

func approvalFailureText(err error) string {
	switch {
	case errors.Is(err, errApprovalDenied):
		return "The member rejected this desktop action."
	case errors.Is(err, errApprovalUnavailable):
		return "This high-risk desktop action needs member approval, but approval is unavailable: " + err.Error()
	default:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "Desktop approval did not complete: " + err.Error()
		}
		return "Desktop approval failed: " + strings.TrimSpace(err.Error())
	}
}
