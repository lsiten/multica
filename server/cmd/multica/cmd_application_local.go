package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

func localApplicationControl(ctx context.Context, httpClient *http.Client, address, profile string, client *cli.APIClient, appID string, body map[string]any) (map[string]any, bool, error) {
	action, _ := body["action"].(string)
	if action != "start" && action != "stop" && action != "restart" {
		return nil, false, nil
	}
	if strings.HasPrefix(client.Token, cli.TaskTokenPrefix) || client.Token == "" {
		return nil, false, nil
	}
	runtimeID, _ := body["runtime_id"].(string)
	idempotencyKey, _ := body["idempotency_key"].(string)
	if runtimeID == "" || idempotencyKey == "" {
		return nil, false, nil
	}
	request := func(method string, payload any, out any) (int, error) {
		var raw []byte
		var err error
		if payload != nil {
			raw, err = json.Marshal(payload)
			if err != nil {
				return 0, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, address+"/applications/local", bytes.NewReader(raw))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+client.Token)
		req.Header.Set("X-Multica-Profile", profile)
		req.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return response.StatusCode, fmt.Errorf("local application request failed (%d)", response.StatusCode)
		}
		return response.StatusCode, json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(out)
	}
	var inventory struct {
		Applications []struct {
			WorkspaceID   string `json:"workspace_id"`
			RuntimeID     string `json:"runtime_id"`
			ApplicationID string `json:"application_id"`
			Revision      int64  `json:"revision"`
		} `json:"applications"`
	}
	status, err := request(http.MethodGet, nil, &inventory)
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	for _, item := range inventory.Applications {
		if item.WorkspaceID != client.WorkspaceID || item.RuntimeID != runtimeID || item.ApplicationID != appID {
			continue
		}
		revisionRaw, _ := json.Marshal(body["revision"])
		var revision int64
		if json.Unmarshal(revisionRaw, &revision) != nil || revision != item.Revision {
			return nil, false, nil
		}
		commandID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(client.BaseURL+"\x00"+client.WorkspaceID+"\x00"+appID+"\x00"+idempotencyKey)).String()
		input := map[string]any{"workspace_id": client.WorkspaceID, "runtime_id": runtimeID, "application_id": appID, "revision": revision, "action": action, "command_id": commandID}
		var result map[string]any
		_, err := request(http.MethodPost, input, &result)
		if err != nil {
			return nil, true, err
		}
		if result["command_id"] != commandID {
			return nil, true, fmt.Errorf("local application receipt identity changed")
		}
		result["id"], result["state"], result["local"] = commandID, "completed", true
		return result, true, nil
	}
	return nil, false, nil
}

func localApplicationHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
