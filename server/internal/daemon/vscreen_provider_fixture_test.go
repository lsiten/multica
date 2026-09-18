//go:build darwin || linux

package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// This child speaks the Claude stream contract but only executes owned loopback
// fixtures. It never resolves an installed Agent or contacts a model service.
func runVscreenProviderFixture() int {
	for _, arg := range os.Args[2:] {
		if arg == "--version" {
			fmt.Println("2.1.0 (owned fixture)")
			return 0
		}
	}
	var configPath string
	for i, arg := range os.Args {
		if arg == "--mcp-config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return 21
	}
	var config struct {
		Servers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Servers) != 3 || config.Servers[vscreenMCPName].URL == "" {
		return 22
	}
	if _, ok := config.Servers["owned-runtime"]; !ok {
		return 23
	}
	if _, ok := config.Servers["owned-agent"]; !ok {
		return 24
	}
	if _, err = bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		return 25
	}
	client := &http.Client{Timeout: 5 * time.Second}
	notify := func(stage string) error {
		response, err := client.Post(os.Getenv("VSCREEN_FIXTURE_ENDPOINT")+"/fixture/"+stage, "application/json", strings.NewReader(`{}`))
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("fixture boundary rejected")
		}
		return nil
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM)
	defer signal.Stop(stopped)
	go func() {
		<-stopped
		fmt.Println(`{"type":"assistant","message":{"role":"assistant","model":"owned-fixture","content":[{"type":"text","text":"owned-provider-cleanup-tail"}],"usage":{"input_tokens":7,"output_tokens":3}}}`)
		if notify("provider-stopped") != nil {
			os.Exit(26)
		}
		os.Exit(0)
	}()
	fmt.Println(`{"type":"system","session_id":"owned-vscreen-session"}`)
	fmt.Println(`{"type":"assistant","message":{"role":"assistant","model":"owned-fixture","content":[{"type":"tool_use","id":"owned-call","name":"mcp__multica-vscreen__vscreen_key","input":{"text":"private-gui-marker"}}],"usage":{"input_tokens":11,"output_tokens":2}}}`)
	if notify("provider-started") != nil {
		return 27
	}
	call := func(name string, args any) (map[string]any, error) {
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		response, err := client.Post(config.Servers[vscreenMCPName].URL, "application/json", bytes.NewReader(request))
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		var body struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
			return nil, err
		}
		if body.Result.IsError || len(body.Result.Content) == 0 {
			return nil, errors.New("managed tool refused")
		}
		var result map[string]any
		err = json.Unmarshal([]byte(body.Result.Content[0].Text), &result)
		return result, err
	}
	lease, err := call("vscreen_acquire", map[string]any{"request_id": "owned-ticket"})
	if err != nil {
		return 28
	}
	tx := lease["transaction_id"]
	observation, err := call("vscreen_launch_app", map[string]any{"transaction_id": tx, "bundle_id": "owned.fixture"})
	if err != nil {
		return 29
	}
	_, _ = call("vscreen_key", map[string]any{"transaction_id": tx, "window_handle": "wire-window", "snapshot_revision": observation["snapshot_revision"], "action_id": "unknown-key", "sequence": 1, "action": map[string]any{"kind": "key", "key": map[string]any{"key": "Enter"}}})
	// The provider has no authoritative successful result; only its owned process
	// cancellation and cleanup may settle this invocation.
	select {
	case <-time.After(10 * time.Second):
		return 30
	}
}
