package runtimeproc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTask20Child(t *testing.T) {
	if os.Getenv("TASK20_CHILD") != "1" {
		return
	}
	b, err := ReadBootstrap(os.Stdin, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	guard := make(chan struct{}, 1)
	guard <- struct{}{}
	var waiting atomic.Bool
	var service *Service
	s, err := NewService(Config{Bootstrap: b, Capabilities: []string{"begin", "prepare", "physical_finish_confirm"}, ConcurrentCapabilities: []string{"physical_finish_confirm"}, ReadCapabilities: []string{"fixture_state"}, ReadHandler: func(context.Context, Request) (json.RawMessage, *Error) {
		service.admission.Lock()
		writers := service.lifecycleWaiting
		service.admission.Unlock()
		return json.RawMessage(fmt.Sprintf(`{"waiting":%t,"pid":%d,"lifecycle_waiting":%d}`, waiting.Load(), os.Getpid(), writers)), nil
	}, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
		switch r.Operation {
		case "begin":
			select {
			case <-guard:
			case <-ctx.Done():
				return nil, nil
			}
		case "prepare":
			waiting.Store(true)
			defer waiting.Store(false)
			select {
			case <-guard:
			case <-ctx.Done():
				return nil, nil
			}
			guard <- struct{}{}
		case "physical_finish_confirm":
			select {
			case guard <- struct{}{}:
			case <-ctx.Done():
				return nil, nil
			}
		}
		return json.RawMessage(fmt.Sprintf(`{"pid":%d}`, os.Getpid())), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	service = s
	if err = s.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentProcessCurlGuardConfirmation(t *testing.T) {
	directory := os.Getenv("TASK20_EVIDENCE_DIR")
	if directory == "" {
		t.Skip("explicit private fixture evidence directory required")
	}
	cfg := launchFixture(t, "normal")
	cfg.Environment = map[string]string{"TASK20_CHILD": "1"}
	p, err := start(t.Context(), cfg, []string{"-test.run=^TestTask20Child$"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	record, err := ReadRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	auth := filepath.Join(private, "curl.conf")
	if err = os.WriteFile(auth, []byte("header = \"Authorization: Bearer "+record.Token+"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	type curlResult struct {
		raw      []byte
		response Response
		err      error
	}
	curl := func(name string, r Request, authenticated bool) curlResult {
		body, _ := json.Marshal(r)
		path := filepath.Join(private, name+".json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			return curlResult{err: err}
		}
		args := []string{"-i", "--silent", "--show-error", "--max-time", "10", "--header", "Content-Type: application/json", "--data-binary", "@" + path}
		if authenticated {
			args = append(args, "--config", auth)
		}
		args = append(args, record.Address+"/rpc")
		raw, err := exec.CommandContext(t.Context(), "curl", args...).CombinedOutput()
		result := curlResult{raw: raw, err: err}
		if index := bytes.Index(raw, []byte("\r\n\r\n")); index >= 0 {
			_ = json.Unmarshal(raw[index+4:], &result.response)
		}
		return result
	}
	var transcript strings.Builder
	capture := func(name string, result curlResult) {
		t.Helper()
		transcript.WriteString("scenario: " + name + "\ncommand: curl -i --silent --show-error --max-time 10 --header Content-Type:application/json --data-binary @private-request --config private-credential " + record.Address + "/rpc\n")
		transcript.Write(result.raw)
		transcript.WriteString("\n")
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	rejected := curl("unauthorized", request(t, p.Client, "health", ""), false)
	capture("unauthorized", rejected)
	if !bytes.Contains(rejected.raw, []byte("401")) {
		t.Fatal("unauthorized accepted")
	}
	begin := curl("begin", request(t, p.Client, "begin", `{}`), true)
	capture("begin", begin)
	if begin.response.Receipt == nil || begin.response.Receipt.State != "completed" {
		t.Fatal("begin failed")
	}
	prepare := request(t, p.Client, "prepare", `{}`)
	prepared := make(chan curlResult, 1)
	go func() { prepared <- curl("prepare", prepare, true) }()
	var childPID int
	awaitConcurrent(t, func() bool {
		raw, err := p.Client.Read(t.Context(), "fixture_state", nil)
		if err != nil {
			return false
		}
		var state struct {
			Waiting bool `json:"waiting"`
			PID     int  `json:"pid"`
		}
		_ = json.Unmarshal(raw, &state)
		childPID = state.PID
		return state.Waiting
	})
	ackRequest := request(t, p.Client, "acknowledge", "")
	acked := make(chan curlResult, 1)
	go func() { acked <- curl("queued-ack", ackRequest, true) }()
	awaitConcurrent(t, func() bool {
		raw, err := p.Client.Read(t.Context(), "fixture_state", nil)
		if err != nil {
			return false
		}
		var state struct {
			Writers int `json:"lifecycle_waiting"`
		}
		_ = json.Unmarshal(raw, &state)
		return state.Writers == 1
	})
	confirm := curl("confirm", request(t, p.Client, "physical_finish_confirm", `{}`), true)
	capture("confirm while prepare held by domain guard", confirm)
	result := <-prepared
	capture("prepare released by independent confirmation", result)
	if confirm.response.Receipt == nil || confirm.response.Receipt.State != "completed" || result.response.Receipt == nil || result.response.Receipt.State != "completed" || childPID == os.Getpid() {
		t.Fatal("real process confirmation did not release ordinary operation")
	}
	ackResult := <-acked
	capture("queued ACK rejected after concurrent completion changed fence", ackResult)
	if ackResult.response.Error == nil || ackResult.response.Error.Code != "stale_fence" {
		t.Fatal("queued ACK crossed concurrent receipts")
	}
	duplicate := curl("duplicate", prepare, true)
	capture("identical request returns completed receipt", duplicate)
	if duplicate.response.Receipt == nil || duplicate.response.Receipt.State != "completed" {
		t.Fatal("duplicate receipt missing")
	}
	conflict := prepare
	conflict.Payload = json.RawMessage(`{"different":true}`)
	conflicted := curl("conflict", conflict, true)
	capture("same ID different payload", conflicted)
	if conflicted.response.Error == nil || conflicted.response.Error.Code != "request_conflict" {
		t.Fatal("conflicting request accepted")
	}
	queried := curl("query", request(t, p.Client, "query_operation", fmt.Sprintf(`{"request_id":%q}`, prepare.RequestID)), true)
	capture("completed operation remains queryable", queried)
	if queried.response.Receipt == nil || queried.response.Receipt.State != "completed" {
		t.Fatal("receipt retired by stale ACK")
	}
	stop := curl("stop", request(t, p.Client, "stop", ""), true)
	capture("stop", stop)
	if stop.response.Status.State != "stopped" {
		t.Fatal("stop failed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err = p.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(record.Address, "http://"), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("child listener survived")
	}
	if err = os.WriteFile(filepath.Join(directory, "curl-surface.log"), []byte(transcript.String()), 0600); err != nil {
		t.Fatal(err)
	}
	proof, _ := json.MarshalIndent(map[string]any{"child_pid": childPID, "controller_pid": os.Getpid(), "waited": true, "listener_closed": true, "prepare_completed": true, "confirm_completed": true, "queued_ack_stale": true, "duplicate_query_completed": true, "payload_conflict_rejected": true}, "", "  ")
	if err = os.WriteFile(filepath.Join(directory, "curl-cleanup.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual child PID%d confirmed held guard through curl; prepare completed; authenticated stop waited; listener closed", childPID)
}
