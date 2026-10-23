package runtimeproc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSelectiveAcknowledgementPreservesUnconsumedReceipts(t *testing.T) {
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"work"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return nil, nil }})
	first := request(t, c, "work", "")
	call(t, c, first)
	second := request(t, c, "work", "")
	call(t, c, second)
	s.mu.Lock()
	s.record.Operations["1:abandoned"] = Receipt{RequestID: "1:abandoned", State: "pending"}
	s.mu.Unlock()
	ack := request(t, c, "acknowledge", `{"request_ids":["`+first.RequestID+`"]}`)
	out := call(t, c, ack)
	if out.Status.Receipts != (ReceiptUsage{Total: 3, Pending: 1, Completed: 2, Limit: 128}) || out.Receipt.Operation != "acknowledge" {
		t.Fatalf("missing receipt diagnostics: %+v", out)
	}
	if out.Status.ReplayEpoch != 2 {
		t.Fatalf("epoch=%d", out.Status.ReplayEpoch)
	}
	for _, id := range []string{second.RequestID, "1:abandoned", ack.RequestID} {
		if _, err := c.QueryOperation(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.QueryOperation(context.Background(), first.RequestID); err == nil {
		t.Fatal("retired receipt remains")
	}
	if got := call(t, c, second); got.Receipt.State != "completed" {
		t.Fatal(got)
	}
	if got := call(t, c, ack); got.Receipt.State != "completed" {
		t.Fatal(got)
	}
}

func TestSelectiveAcknowledgementRejectsInvalidWithoutPersistence(t *testing.T) {
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t)})
	s.mu.Lock()
	s.record.Operations["1:pending"] = Receipt{State: "pending"}
	s.record.Operations["1:done"] = Receipt{State: "completed"}
	s.mu.Unlock()
	before, err := readPrivate(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ payload, code string }{
		{`{}`, "malformed"}, {`[]`, "malformed"}, {`{"request_ids":[]}`, "malformed"}, {`{"request_ids":null}`, "malformed"}, {`{"request_ids":[""]}`, "malformed"}, {`{"request_ids":["1:done","1:done"]}`, "malformed"}, {`{"request_ids":["1:missing"]}`, "not_found"}, {`{"request_ids":["1:done","1:pending"]}`, "uncertain_operation"}, {`{"request_ids":["1:done"],"extra":true}`, "malformed"},
	} {
		r := request(t, c, "acknowledge", tc.payload)
		wantCode(t, c, r, tc.code)
		after, err := readPrivate(s.path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("invalid ACK persisted changes: %v", err)
		}
		s.mu.Lock()
		if len(s.record.Operations) != 2 || s.record.Fence.Revision != 1 || s.record.ReplayEpoch != 1 {
			t.Fatal("invalid acknowledgement changed journal")
		}
		s.mu.Unlock()
	}
}

func TestSelectiveAcknowledgementMarkersStayBounded(t *testing.T) {
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"work"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return nil, nil }})
	for range 12 {
		work := request(t, c, "work", "")
		call(t, c, work)
		ack := request(t, c, "acknowledge", `{"request_ids":["`+work.RequestID+`"]}`)
		out := call(t, c, ack)
		retry := call(t, c, ack)
		if retry.Status.Fence != out.Status.Fence {
			t.Fatal("retry changed revision")
		}
		s.mu.Lock()
		if len(s.record.Operations) != 1 || s.record.AcknowledgementID != ack.RequestID {
			t.Fatal("ack markers accumulated")
		}
		s.mu.Unlock()
	}
}

func TestSelectiveAcknowledgementRecoversFullJournalWithPending(t *testing.T) {
	b := testBootstrap(t)
	s, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"work"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return nil, nil }})
	s.mu.Lock()
	for i := range maxOperations + 17 {
		id := fmt.Sprintf("1:receipt-%d", i)
		state := "completed"
		if i == 0 {
			state = "pending"
		}
		s.record.Operations[id] = Receipt{RequestID: id, State: state, Operation: "work"}
	}
	s.mu.Unlock()
	wantCode(t, c, request(t, c, "work", ""), "resource_exhausted")
	ids := make([]string, 0, maxOperations+16)
	for i := 1; i < maxOperations+17; i++ {
		ids = append(ids, fmt.Sprintf("1:receipt-%d", i))
	}
	raw, _ := json.Marshal(AcknowledgePayload{RequestIDs: ids})
	call(t, c, request(t, c, "acknowledge", string(raw)))
	call(t, c, request(t, c, "work", ""))
	disk, err := ReadRecord(b.Root, b.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if disk.Operations["1:receipt-0"].State != "pending" || len(disk.Operations) != 3 {
		t.Fatalf("journal=%+v", disk)
	}
	s.mu.Lock()
	for i := range maxOperations + 18 {
		id := fmt.Sprintf("2:full-%d", i)
		s.record.Operations[id] = Receipt{State: "completed"}
	}
	s.mu.Unlock()
	wantCode(t, c, request(t, c, "acknowledge", `{"request_ids":["2:full-0"]}`), "resource_exhausted")
}

func TestAcknowledgementCommitFailurePreservesMetadata(t *testing.T) {
	b := testBootstrap(t)
	s, err := NewService(Config{Bootstrap: b})
	if err != nil {
		t.Fatal(err)
	}
	defer s.lock.Close()
	s.record.AcknowledgementID = "1:old-ack"
	s.record.Operations = map[string]Receipt{"1:old-ack": {State: "completed"}, "1:done": {State: "completed"}, "1:ack": {State: "pending"}}
	s.path = filepath.Join(t.TempDir(), "missing", "owner.json")
	receipt := Receipt{State: "completed"}
	err = s.completeLocked(Request{Operation: "acknowledge", RequestID: "1:ack"}, &receipt, []string{"1:done"})
	if err == nil || s.record.Fence != b.Fence || s.record.ReplayEpoch != 1 || s.record.AcknowledgementID != "1:old-ack" || len(s.record.Operations) != 3 || s.record.Operations["1:ack"].State != "pending" || s.record.State != "suspect" {
		t.Fatalf("unsafe failure: %+v %v", s.record, err)
	}
}
