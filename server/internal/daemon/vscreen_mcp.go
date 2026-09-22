package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const vscreenMCPName = "multica-vscreen"
const vscreenMCPMaxRequest = 64 << 10

type vscreenToolInvoker func(context.Context, string, json.RawMessage) ([]map[string]any, error)

type vscreenMCP struct {
	path     string
	invoke   vscreenToolInvoker
	server   *http.Server
	listener net.Listener
	once     sync.Once
	done     chan struct{}
}

func startVscreenMCP(ctx context.Context, invoke vscreenToolInvoker) (json.RawMessage, *vscreenMCP, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	token, err := randomBrokerToken()
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	s := &vscreenMCP{path: "/" + token, invoke: invoke, listener: listener, done: make(chan struct{})}
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { defer close(s.done); _ = s.server.Serve(listener) }()
	context.AfterFunc(ctx, func() { s.Close() })
	cfg, err := json.Marshal(map[string]any{"mcpServers": map[string]any{vscreenMCPName: map[string]any{"type": "http", "url": "http://" + listener.Addr().String() + s.path}}})
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	return cfg, s, nil
}
func (s *vscreenMCP) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		_ = s.server.Close()
		<-s.done
	})
}
func (s *vscreenMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != s.path || r.Header.Get("Origin") != "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Context().Err() != nil {
		w.WriteHeader(http.StatusGone)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, vscreenMCPMaxRequest))
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var req pluginHookMCPRequest
	if strictVscreenJSON(raw, &req) != nil || req.JSONRPC != "2.0" {
		writePluginHookMCPError(w, nil, -32600, "invalid request")
		return
	}
	switch req.Method {
	case "initialize":
		writePluginHookMCPResult(w, req.ID, map[string]any{"protocolVersion": pluginHookMCPProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": vscreenMCPName, "version": "1"}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writePluginHookMCPResult(w, req.ID, map[string]any{"tools": vscreenToolDescriptors()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if strictVscreenJSON(req.Params, &params) != nil {
			writePluginHookMCPError(w, req.ID, -32602, "invalid tool parameters")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		content, err := s.invoke(ctx, params.Name, params.Arguments)
		if err != nil {
			writePluginHookMCPResult(w, req.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": safeVscreenToolError(err)}}})
			return
		}
		writePluginHookMCPResult(w, req.ID, map[string]any{"content": content})
	default:
		writePluginHookMCPError(w, req.ID, -32601, "unsupported method")
	}
}
func strictVscreenJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}
func vscreenToolDescriptors() []map[string]any {
	names := []string{"status", "acquire", "release", "list_apps", "launch_app", "observe", "click", "drag", "scroll", "type", "key"}
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		props := map[string]any{}
		required := []string{}
		add := func(key, kind string) { props[key] = map[string]any{"type": kind}; required = append(required, key) }
		switch name {
		case "acquire":
			add("request_id", "string")
			props["intent"] = map[string]any{"type": "string"}
		case "status":
		default:
			add("transaction_id", "string")
		}
		switch name {
		case "launch_app":
			add("bundle_id", "string")
			props["files"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		case "observe":
			props["window_handle"] = map[string]any{"type": "string"}
		case "click", "drag", "scroll", "type", "key":
			add("window_handle", "string")
			add("snapshot_revision", "integer")
			add("action_id", "string")
			add("sequence", "integer")
			props["action"] = vscreenActionSchema(name)
			required = append(required, "action")
		}
		description := "Managed runtime GUI " + name + ". Never use other desktop automation. Reobserve after changes; never replay uncertain actions."
		if name == "list_apps" {
			description = "List NSWorkspace-resolved apps in standard Applications folders. Truncated inventories are incomplete. Reuse a matching handle from managed_windows instead of relaunching an owned running app. Other running apps require local human adoption; inventory does not certify background input. Known bundle IDs outside these folders may be passed to launch_app."
		}
		if name == "acquire" || name == "status" {
			description += " A current lease returns managed_windows entries with window_handle and bundle_id only. Listing is not an observation and does not authorize input; select a handle and freshly observe it."
		}
		if name == "observe" {
			description += " Omit window_handle or use an empty string for a display-only PNG plus managed_windows; it cannot authorize actions. An explicit managed window handle returns fresh AX elements and snapshot_revision for actions."
		}
		if name == "key" || name == "scroll" || name == "drag" {
			description += " Requires independently verified app/OS/action certification. Without it, this tool stops automation for human intervention."
		}
		result = append(result, map[string]any{"name": "vscreen_" + name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}})
	}
	return result
}

func vscreenActionSchema(kind string) map[string]any {
	scalar := func(t string) map[string]any { return map[string]any{"type": t} }
	object := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	point := object(map[string]any{"x": scalar("number"), "y": scalar("number")}, "x", "y")
	var payload map[string]any
	switch kind {
	case "click":
		payload = object(map[string]any{"element_handle": scalar("string"), "position": point})
		payload["oneOf"] = []any{map[string]any{"required": []string{"element_handle"}}, map[string]any{"required": []string{"position"}}}
	case "type":
		payload = object(map[string]any{"element_handle": scalar("string"), "text": map[string]any{"type": "string", "maxLength": 8192}}, "element_handle", "text")
	case "key":
		payload = object(map[string]any{"key": scalar("string"), "modifiers": map[string]any{"type": "array", "maxItems": 4, "items": map[string]any{"type": "string", "enum": []string{"shift", "control", "alt", "meta"}}}}, "key")
	case "scroll":
		payload = object(map[string]any{"position": point, "delta_x": scalar("number"), "delta_y": scalar("number")}, "position", "delta_x", "delta_y")
	case "drag":
		payload = object(map[string]any{"from": point, "to": point, "duration_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 3000}}, "from", "to", "duration_ms")
	}
	return object(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{kind}}, kind: payload}, "kind", kind)
}
