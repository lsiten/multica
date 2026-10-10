package protocol

import "testing"

func TestExecutionCallbackRouteContract(t *testing.T) {
	task := "11111111-1111-4111-8111-111111111111"
	comment := "22222222-2222-4222-8222-222222222222"
	for _, op := range []string{"start", "status", "prepare-lease", "supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events"} {
		method := "POST"
		if op == "status" {
			method = "GET"
		}
		resource := ""
		if op == "supplements/ack" {
			resource = comment
		}
		path, ok := ExecutionCallbackPath(method, op, task, resource)
		if !ok || !ExecutionOperationAllowed(method, op) {
			t.Fatalf("registered operation unavailable: %s", op)
		}
		gotTask, gotOp, gotResource, ok := ExecutionCallbackOperation(method, path)
		if !ok || gotTask != task || gotOp != op || gotResource != resource {
			t.Fatalf("route roundtrip failed: %s", path)
		}
	}
	base := "/api/daemon/tasks/" + task
	for _, test := range []struct{ method, path string }{{"GET", base + "/supplements/claim"}, {"POST", base + "/supplements/ack"}, {"POST", base + "/supplements/bad/ack"}, {"POST", base + "/supplements/" + comment + "/ack/more"}, {"POST", base + "//worktree-delivery"}, {"POST", base + "/%77orktree-delivery"}, {"POST", base + "/project-graph%2fevents"}, {"POST", base + "/../worktree-delivery"}, {"POST", base + "/identity/email"}, {"POST", "/api/human-requests/"}, {"HEAD", base + "/status"}} {
		if _, _, _, ok := ExecutionCallbackOperation(test.method, test.path); ok {
			t.Fatalf("unregistered literal route accepted: %+v", test)
		}
	}
	if _, ok := ExecutionCallbackPath("POST", "supplements/ack", task, ""); ok {
		t.Fatal("ack resource omitted")
	}
}
