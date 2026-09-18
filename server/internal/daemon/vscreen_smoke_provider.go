package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const VscreenSmokeProviderCommand = "internal-vscreen-takeover-provider"
const smokePrivateNonceEnv = "MULTICA_VSCREEN_SMOKE_NONCE"
const smokeProviderNonceEnv = "VSCREEN_SMOKE_PROVIDER_NONCE"

type smokeProviderConfig struct {
	GUIAuthorized                                                                 bool
	Nonce, TaskID, WorkspaceID, RuntimeID, BundleID, Stage, Endpoint, EvidenceDir string
	OwnerPID                                                                      int
}
type smokeProviderEvent struct {
	TaskID   string `json:"task_id"`
	Stage    string `json:"stage"`
	Window   string `json:"window"`
	Revision uint64 `json:"revision"`
	Element  string `json:"element"`
}
type smokeObservation struct {
	Window        string `json:"window_handle"`
	Revision      uint64 `json:"snapshot_revision"`
	Width, Height int
	Elements      []appcontrol.Element `json:"elements"`
}

// RunVscreenSmokeProvider is a reserved same-binary, scope-bound fixture entry.
// It speaks the production Claude protocol but never executes a model or shell.
func RunVscreenSmokeProvider(ctx context.Context, configPath string, args []string, in io.Reader, out io.Writer) error {
	var config smokeProviderConfig
	if err := readSmokePrivateJSON(configPath, &config); err != nil {
		if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
			return errors.New("gui_not_authorized")
		}
		return err
	}
	if !config.GUIAuthorized {
		return errors.New("gui_not_authorized")
	}
	if len(config.Nonce) < 32 || subtle.ConstantTimeCompare([]byte(config.Nonce), []byte(os.Getenv(smokeProviderNonceEnv))) != 1 || config.OwnerPID != os.Getppid() || config.TaskID != os.Getenv("MULTICA_TASK_ID") || config.WorkspaceID != os.Getenv("MULTICA_WORKSPACE_ID") || config.RuntimeID == "" || !ownedSmokeBundleID(config.BundleID) || !filepath.IsAbs(config.EvidenceDir) {
		return errors.New("invalid_smoke_provider_scope")
	}
	if config.Stage != "source" && config.Stage != "continuation" {
		return errors.New("invalid_smoke_provider_stage")
	}
	return runSmokeProvider(ctx, config, args, in, out)
}
func runSmokeProvider(ctx context.Context, c smokeProviderConfig, args []string, in io.Reader, out io.Writer) error {
	var mcpPath string
	for i, arg := range args {
		if arg == "--mcp-config" && i+1 < len(args) {
			mcpPath = args[i+1]
		}
	}
	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		return err
	}
	var cfg struct {
		Servers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil || cfg.Servers[vscreenMCPName].URL == "" {
		return errors.New("managed_mcp_missing")
	}
	endpoint := cfg.Servers[vscreenMCPName].URL
	for _, target := range []string{endpoint, c.Endpoint} {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
			return errors.New("smoke_loopback_required")
		}
	}
	if _, err = bufio.NewReader(in).ReadString('\n'); err != nil {
		return err
	}
	encode := json.NewEncoder(out)
	if err = encode.Encode(map[string]any{"type": "system", "session_id": "smoke-session-" + c.TaskID}); err != nil {
		return err
	}
	notify := func(stage string, o smokeObservation) error {
		return postSmokeEvent(ctx, c, smokeProviderEvent{TaskID: c.TaskID, Stage: stage, Window: o.Window, Revision: o.Revision, Element: smokeTextElement(o)})
	}
	call := func(name string, args any) (json.RawMessage, []byte, error) {
		return callSmokeMCP(ctx, endpoint, name, args)
	}
	leaseRaw, _, err := call("vscreen_acquire", map[string]any{"request_id": "smoke-" + c.Stage})
	if err != nil {
		return err
	}
	var lease struct {
		Transaction string `json:"transaction_id"`
		Windows     []struct {
			Handle   string `json:"window_handle"`
			BundleID string `json:"bundle_id"`
		} `json:"managed_windows"`
	}
	if json.Unmarshal(leaseRaw, &lease) != nil || lease.Transaction == "" {
		return errors.New("smoke_lease_missing")
	}
	window := ""
	if c.Stage == "continuation" {
		for _, owned := range lease.Windows {
			if owned.BundleID == c.BundleID {
				if window != "" {
					return errors.New("ambiguous_owned_fixture_window")
				}
				window = owned.Handle
			}
		}
		if window == "" {
			return errors.New("owned_window_not_discoverable")
		}
	}
	tool := "vscreen_observe"
	arguments := map[string]any{"transaction_id": lease.Transaction, "window_handle": window}
	if c.Stage == "source" {
		tool = "vscreen_launch_app"
		arguments = map[string]any{"transaction_id": lease.Transaction, "bundle_id": c.BundleID}
	}
	observation, pixels, err := call(tool, arguments)
	if err != nil {
		return err
	}
	var observed smokeObservation
	if json.Unmarshal(observation, &observed) != nil || observed.Window == "" || observed.Revision == 0 {
		return errors.New("smoke_observation_missing")
	}
	if err = writeTakeoverPNG(c.EvidenceDir, "takeover-"+c.Stage+".png", pixels); err != nil {
		return err
	}
	if err = notify(c.Stage+"-observed", observed); err != nil {
		return err
	}
	action := protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "MulticaSmokeUnsupportedKey"}}
	actionName := "vscreen_key"
	if c.Stage == "continuation" {
		if smokeTextElement(observed) == "" {
			return errors.New("owned_text_element_missing")
		}
		actionName = "vscreen_type"
		action = protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: smokeTextElement(observed), Text: "Multica continuation verified"}}
	}
	if err = encode.Encode(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "model": "owned-smoke-fixture", "content": []map[string]any{{"type": "tool_use", "id": "smoke-action", "name": "mcp__multica-vscreen__" + actionName, "input": map[string]any{}}}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}}); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	_, _, actionErr := call(actionName, map[string]any{"transaction_id": lease.Transaction, "window_handle": observed.Window, "snapshot_revision": observed.Revision, "action_id": "smoke-" + c.Stage + "-action", "sequence": 1, "action": action})
	if c.Stage == "source" {
		if actionErr == nil {
			return errors.New("unsupported_smoke_action_was_delivered")
		}
		select {
		case <-signals:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("provider_not_stopped")
		}
		if err = encode.Encode(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "smoke provider cleanup drained"}}}}); err != nil {
			return err
		}
		return notify("provider-stopped", observed)
	}
	if actionErr != nil {
		return actionErr
	}
	if err = notify("continuation-input", observed); err != nil {
		return err
	}
	_, _, err = call("vscreen_release", map[string]any{"transaction_id": lease.Transaction})
	if err != nil {
		return err
	}
	return encode.Encode(map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": "smoke-session-" + c.TaskID, "result": "owned continuation finished"})
}
func smokeTextElement(o smokeObservation) string {
	for _, e := range o.Elements {
		if e.Title == "Multica smoke text" && e.SetValue {
			return e.Handle
		}
	}
	return ""
}
func postSmokeEvent(ctx context.Context, c smokeProviderConfig, event smokeProviderEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/smoke/event", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.Nonce)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("smoke_event_refused_%d", response.StatusCode)
	}
	return nil
}
func callSmokeMCP(ctx context.Context, endpoint, name string, args any) (json.RawMessage, []byte, error) {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	response, err := (&http.Client{Timeout: 22 * time.Second}).Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	var reply struct {
		Result struct {
			IsError bool                                          `json:"isError"`
			Content []struct{ Type, Text, Data, MimeType string } `json:"content"`
		} `json:"result"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&reply); err != nil {
		return nil, nil, err
	}
	if reply.Result.IsError {
		return nil, nil, errors.New("managed_smoke_tool_refused")
	}
	var metadata json.RawMessage
	var pixels []byte
	for _, part := range reply.Result.Content {
		if part.Type == "text" {
			metadata = json.RawMessage(part.Text)
		}
		if part.Type == "image" && part.MimeType == "image/png" {
			pixels, err = base64.StdEncoding.DecodeString(part.Data)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if len(metadata) == 0 {
		return nil, nil, errors.New("smoke_tool_result_missing")
	}
	return metadata, pixels, nil
}
func writeTakeoverPNG(directory, name string, pixels []byte) error {
	decoded, err := png.Decode(bytes.NewReader(pixels))
	if err != nil || decoded.Bounds().Dx() < 1 || decoded.Bounds().Dy() < 1 {
		return errors.New("smoke_png_invalid")
	}
	return os.WriteFile(filepath.Join(directory, name), pixels, 0600)
}

func ownedSmokeBundleID(value string) bool {
	suffix, ok := strings.CutPrefix(value, "ai.multica.smoke.")
	if !ok || len(suffix) != 32 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}
