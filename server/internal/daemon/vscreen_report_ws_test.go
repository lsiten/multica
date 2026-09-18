package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenReportActualWSKeepsQueryPumpResponsive(t *testing.T) {
	config, results := reportTestConfig(t)
	config.AckTimeout = time.Second
	r := reportTestNew(t, config)
	serverResult := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, http.Header{protocol.DaemonGenerationHeader: []string{"server-generation"}})
		if err != nil {
			serverResult <- err
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var frame protocol.Message
		if err = conn.ReadJSON(&frame); err != nil {
			serverResult <- err
			return
		}
		if frame.Type != protocol.EventVscreenIntervention {
			serverResult <- vscreenReportError("invalid_report")
			return
		}
		var report protocol.VscreenIntervention
		if err = json.Unmarshal(frame.Payload, &report); err != nil {
			serverResult <- err
			return
		}
		query := protocol.VscreenQuery{VscreenEnvelope: report.VscreenEnvelope, Kind: "state"}
		query.RequestID = "native-query"
		raw, _ := json.Marshal(query)
		if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQuery, Payload: raw}); err != nil {
			serverResult <- err
			return
		}
		if err = conn.ReadJSON(&frame); err != nil {
			serverResult <- err
			return
		}
		var proof protocol.VscreenQueryResult
		if frame.Type != protocol.EventVscreenQueryResult || json.Unmarshal(frame.Payload, &proof) != nil || proof.VscreenEnvelope != query.VscreenEnvelope {
			serverResult <- vscreenReportError("query_pump_blocked")
			return
		}
		ack := protocol.VscreenInterventionAck{VscreenEnvelope: report.VscreenEnvelope, InterventionID: report.InterventionID, Accepted: true, Version: 1}
		raw, _ = json.Marshal(ack)
		serverResult <- conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenInterventionAck, Payload: raw})
	}))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	writes := make(chan *wsOutbound, 8)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for out := range writes {
			if out.beginWrite() {
				if conn.WriteMessage(websocket.TextMessage, out.data) != nil {
					return
				}
			}
		}
	}()
	enqueue := func(raw []byte) (*wsOutbound, error) {
		out := &wsOutbound{data: raw}
		select {
		case writes <- out:
			return out, nil
		default:
			return nil, errWSRPCWriteBufferFull
		}
	}
	binding := r.Bind(response.Header.Get(protocol.DaemonGenerationHeader), enqueue)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			var frame protocol.Message
			if conn.ReadJSON(&frame) != nil {
				return
			}
			switch frame.Type {
			case protocol.EventVscreenInterventionAck:
				r.OnAck(binding, frame.Payload)
			case protocol.EventVscreenQuery:
				var query protocol.VscreenQuery
				if json.Unmarshal(frame.Payload, &query) != nil {
					return
				}
				raw, _ := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Reason: protocol.VscreenRejectionReason("source_unavailable")})
				encoded, _ := json.Marshal(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: raw})
				if _, err := enqueue(encoded); err != nil {
					return
				}
			}
		}
	}()
	if err := r.Queue(context.Background(), reportTestRecord()); err != nil {
		t.Fatal(err)
	}
	if result := reportTestReceive(t, results); result.reason != "" {
		t.Fatalf("report result=%+v", result)
	}
	if err := reportTestReceive(t, serverResult); err != nil {
		t.Fatal(err)
	}
	r.Unbind(binding)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	<-readerDone
	close(writes)
	<-writerDone
	t.Log("real websocket delivered report, native query/result while awaiting ack, then correlated durable acceptance; no read-pump blocking")
}
