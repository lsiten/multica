package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func mcpReadinessRPC(ctx context.Context, client *http.Client, endpoint string, payload any, requireResult bool, sessionID string) (mcpJSONRPCResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return mcpJSONRPCResponse{}, fmt.Errorf("%w: encode", errMCPProtocol)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mcpJSONRPCResponse{}, fmt.Errorf("%w: endpoint", errMCPProtocol)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2024-11-05")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	res, err := client.Do(req)
	if err != nil {
		return mcpJSONRPCResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return mcpJSONRPCResponse{}, fmt.Errorf("mcp status %d", res.StatusCode)
	}
	if !requireResult {
		return mcpJSONRPCResponse{}, nil
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.ID) == 0 {
		return mcpJSONRPCResponse{}, fmt.Errorf("%w: request id", errMCPProtocol)
	}
	var response mcpJSONRPCResponse
	if strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		response, err = readMCPReadinessSSE(res.Body, request.ID)
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(res.Body, mcpReadinessResponseLimit+1))
		if err == nil {
			if len(data) > mcpReadinessResponseLimit {
				return mcpJSONRPCResponse{}, fmt.Errorf("%w: response too large", errMCPProtocol)
			}
			response, err = parseMCPReadinessResponse(data, request.ID)
		}
	}
	if err != nil {
		return mcpJSONRPCResponse{}, err
	}
	response.SessionID = strings.TrimSpace(res.Header.Get("Mcp-Session-Id"))
	return response, nil
}

const mcpReadinessResponseLimit = 256 << 10

func parseMCPReadinessResponse(data []byte, expectedID json.RawMessage) (mcpJSONRPCResponse, error) {
	var response mcpJSONRPCResponse
	if err := json.Unmarshal(data, &response); err != nil || response.JSONRPC != "2.0" || (len(response.Error) > 0 && string(response.Error) != "null") || len(response.Result) == 0 || string(response.Result) == "null" || !bytes.Equal(bytes.TrimSpace(response.ID), bytes.TrimSpace(expectedID)) {
		return mcpJSONRPCResponse{}, fmt.Errorf("%w: json-rpc", errMCPProtocol)
	}
	return response, nil
}

func readMCPReadinessSSE(body io.Reader, expectedID json.RawMessage) (mcpJSONRPCResponse, error) {
	reader := &io.LimitedReader{R: body, N: mcpReadinessResponseLimit + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), mcpReadinessResponseLimit+1)
	var event []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(event) == 0 {
				continue
			}
			var envelope struct {
				Method string          `json:"method"`
				ID     json.RawMessage `json:"id"`
			}
			if json.Unmarshal(event, &envelope) == nil && envelope.Method != "" && len(envelope.ID) == 0 {
				event = nil
				continue
			}
			if reader.N <= 0 {
				return mcpJSONRPCResponse{}, fmt.Errorf("%w: response too large", errMCPProtocol)
			}
			return parseMCPReadinessResponse(event, expectedID)
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if len(event) > 0 {
				event = append(event, '\n')
			}
			event = append(event, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))...)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return mcpJSONRPCResponse{}, fmt.Errorf("%w: response too large", errMCPProtocol)
		}
		return mcpJSONRPCResponse{}, err
	}
	return mcpJSONRPCResponse{}, fmt.Errorf("%w: incomplete SSE response", errMCPProtocol)
}
