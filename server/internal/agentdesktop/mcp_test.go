package agentdesktop

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type approvalStub struct {
	err   error
	calls []approvalRequirement
}

func (s *approvalStub) Approve(_ context.Context, requirement approvalRequirement, _ bool) error {
	s.calls = append(s.calls, requirement)
	return s.err
}

func TestForwardClientMessagesRejectsUnapprovedHighRiskCall(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"desktop_mouse_click","arguments":{"xy":"10,20"}}}` + "\n")
	var output, child bytes.Buffer
	approver := &approvalStub{err: errApprovalDenied}

	err := forwardClientMessages(context.Background(), input, &output, &child, approver, false)
	if err != nil {
		t.Fatal(err)
	}
	if child.Len() != 0 {
		t.Fatalf("rejected call reached agent-desktop: %s", child.String())
	}
	if !strings.Contains(output.String(), `"isError":true`) ||
		!strings.Contains(output.String(), "rejected this desktop action") {
		t.Fatalf("missing MCP rejection: %s", output.String())
	}
	if len(approver.calls) != 1 || approver.calls[0].CallID != "7" {
		t.Fatalf("approval was not bound to the tool call: %+v", approver.calls)
	}
}

func TestForwardClientMessagesForwardsApprovedHighRiskCall(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"desktop_clipboard_clear","arguments":{}}}`
	var output, child bytes.Buffer

	err := forwardClientMessages(context.Background(), strings.NewReader(request+"\n"), &output, &child, &approvalStub{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || child.String() != request+"\n" {
		t.Fatalf("approved call was not forwarded exactly: output=%q child=%q", output.String(), child.String())
	}
}

func TestForwardClientMessagesKeepsObservationDirect(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"desktop_snapshot","arguments":{"app":"Notes"}}}`
	var output, child bytes.Buffer
	approver := &approvalStub{err: errors.New("must not be called")}

	err := forwardClientMessages(context.Background(), strings.NewReader(request+"\n"), &output, &child, approver, false)
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || child.String() != request+"\n" || len(approver.calls) != 0 {
		t.Fatalf("observation was not forwarded directly: output=%q child=%q calls=%d", output.String(), child.String(), len(approver.calls))
	}
}
