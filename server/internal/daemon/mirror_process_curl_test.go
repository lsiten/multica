//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestMirrorProcessCurlSurface(t *testing.T) {
	artifact := os.Getenv("MIRROR_PROCESS_QA_ARTIFACT")
	if artifact == "" {
		t.Skip("explicit curl evidence path required")
	}
	d, client, b := mirrorProcessFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	var transcript strings.Builder
	curl := func(operation string, payload any, auth bool, want int) runtimeproc.Response {
		t.Helper()
		status, err := client.process.Client.Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		request, err := client.process.Client.Request(operation, status.Fence, marshalRaw(payload))
		if err != nil {
			t.Fatal(err)
		}
		var config strings.Builder
		if auth {
			fmt.Fprintf(&config, "header = %q\n", "Authorization: Bearer "+b.Token)
		}
		fmt.Fprintf(&config, "header = %q\ndata = %q\n", "Content-Type: application/json", string(marshalRaw(request)))
		record, err := runtimeproc.ReadRecord(b.Root, b.Identity)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "curl", "-i", "--silent", "--show-error", "--max-time", "5", "--config", "-", record.Address+"/rpc")
		command.Stdin = strings.NewReader(config.String())
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("curl %s: %v", operation, err)
		}
		if !bytes.Contains(raw, []byte(fmt.Sprintf("HTTP/1.1 %d", want))) {
			t.Fatalf("curl %s unexpected HTTP status", operation)
		}
		if bytes.Contains(raw, []byte(b.Token)) {
			t.Fatal("private credential in response")
		}
		fmt.Fprintf(&transcript, "$ curl -i --silent --show-error --max-time 5 --config - <private-fixture-config> [loopback]/rpc\nscenario=%s authenticated=%t\n%s\n", operation, auth, raw)
		parts := bytes.SplitN(raw, []byte("\r\n\r\n"), 2)
		if len(parts) != 2 {
			t.Fatal("invalid HTTP response")
		}
		var response runtimeproc.Response
		if err = json.Unmarshal(parts[1], &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		t.Fatal(err)
	}
	curl("mirror.bind", mirrorProcessRequest{Binding: &mirrorControlBinding{Generation: 1, ServerGeneration: "server", Resources: resources}}, true, 200)
	curl("mirror.inventory", struct{}{}, false, 401)
	response := curl("mirror.inventory", struct{}{}, true, 200)
	var inventory struct {
		PID   int  `json:"pid"`
		Bound bool `json:"bound"`
	}
	if response.Receipt == nil || json.Unmarshal(response.Receipt.Result, &inventory) != nil || inventory.PID == os.Getpid() || !inventory.Bound {
		t.Fatal("actual child identity not proved")
	}
	fmt.Fprintf(&transcript, "controller_pid=%d mirror_pid=%d actual_test_binary=%s\n", os.Getpid(), inventory.PID, os.Args[0])
	accepted := curl("mirror.submit", mirrorProcessRequest{Generation: 1, Operation: "snapshot", WorkspaceID: "ws", RuntimeID: "rt"}, true, 200)
	var pending mirrorJobResult
	if accepted.Receipt == nil || json.Unmarshal(accepted.Receipt.Result, &pending) != nil {
		t.Fatal("query job missing")
	}
	waitMirrorProcess(t, func() bool {
		raw, err := client.process.Client.Read(ctx, "mirror.job", marshalRaw(mirrorProcessRequest{Generation: 1, JobID: pending.JobID}))
		return err == nil && json.Unmarshal(raw, &pending) == nil && pending.Done
	})
	var state protocol.VscreenStateSnapshot
	if pending.Error != "" || json.Unmarshal(pending.Result, &state) != nil || state.Permissions.ScreenRecording != "denied" {
		t.Fatal("fake native permission query failed")
	}
	transcript.WriteString("native query: disabled screen; recording denied; no OS permission request\n")
	peer := newProcessVideoPeer(t, ctx, d, client)
	curl("mirror.submit", mirrorProcessRequest{Generation: 1, Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorOffer, Payload: marshalRaw(peer.offer)})}, true, 200)
	var answer protocol.MirrorAnswerPayload
	for answer.Answer.SDP == "" {
		select {
		case frame := <-peer.frames:
			if frame.Type == protocol.EventMirrorAnswer {
				json.Unmarshal(frame.Payload, &answer)
			}
		case <-ctx.Done():
			t.Fatal("curl offer answer absent")
		}
	}
	if err = peer.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	select {
	case length := <-peer.packets:
		if length == 0 {
			t.Fatal("empty native media")
		}
		fmt.Fprintf(&transcript, "offer delivered through actual parent enqueue; child-native RTP payload=%d bytes (SDP credentials omitted)\n", length)
	case <-ctx.Done():
		t.Fatal("native RTP absent")
	}
	revoke := protocol.MirrorViewerRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", SessionID: peer.grant.SessionID, ViewerID: peer.grant.ViewerID, GrantID: peer.grant.GrantID}
	revokeReply := curl("mirror.submit", mirrorProcessRequest{Generation: 1, Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorViewerRevoke, Payload: marshalRaw(revoke)})}, true, 200)
	json.Unmarshal(revokeReply.Receipt.Result, &pending)
	waitMirrorProcess(t, func() bool {
		raw, err := client.process.Client.Read(ctx, "mirror.job", marshalRaw(mirrorProcessRequest{Generation: 1, JobID: pending.JobID}))
		return err == nil && json.Unmarshal(raw, &pending) == nil && pending.Done
	})
	curl("mirror.cancel_job", mirrorProcessRequest{Generation: 1, JobID: pending.JobID}, true, 200)
	curl("mirror.unbind", mirrorProcessRequest{Generation: 1}, true, 200)
	response = curl("mirror.inventory", struct{}{}, true, 200)
	json.Unmarshal(response.Receipt.Result, &inventory)
	if inventory.Bound {
		t.Fatal("disconnect still authorized child")
	}
	response = curl("stop", struct{}{}, true, 200)
	if response.Status.State != "stopped" {
		t.Fatal("child stop not confirmed")
	}
	if err = client.process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	client.closeOnce.Do(func() { client.cancel(); <-client.done })
	transcript.WriteString("cleanup: durable stopped receipt, actual mirror child Wait exit=0, native cleanup proof written; no screen permission prompt or real agent\n")
	if strings.Contains(transcript.String(), b.Token) {
		t.Fatal("credential leaked to evidence")
	}
	if err = os.WriteFile(artifact, []byte(transcript.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
