package daemon

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"testing"
)

func (f *vscreenToolFixture) ListApps(context.Context, appcontrol.Authority) (appcontrol.AppList, error) {
	return appcontrol.AppList{Apps: []appcontrol.InstalledApp{{BundleID: "org.example.Editor", Name: "Editor", Running: true}}}, nil
}

func TestVscreenMCPAppDiscoveryTransactionAndNoNativeIdentity(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	e := newVscreenExecution(t.Context(), Task{ID: "task"}, a, f, nil)
	defer e.Close()
	if _, err := e.invoke(t.Context(), "vscreen_list_apps", json.RawMessage(`{}`)); err == nil {
		t.Fatal("inventory without transaction")
	}
	got, err := e.invoke(t.Context(), "vscreen_acquire", json.RawMessage(`{"request_id":"discover"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = got
	raw, _ := json.Marshal(map[string]string{"transaction_id": e.lease.TransactionID})
	got, err = e.invoke(t.Context(), "vscreen_list_apps", raw)
	if err != nil {
		t.Fatal(err)
	}
	var list appcontrol.AppList
	if err = json.Unmarshal([]byte(got[0]["text"].(string)), &list); err != nil || len(list.Apps) != 1 || !list.Apps[0].Running {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	forged, _ := json.Marshal(map[string]any{"transaction_id": e.lease.TransactionID, "pid": 123})
	if _, err = e.invoke(t.Context(), "vscreen_list_apps", forged); err == nil {
		t.Fatal("accepted agent PID")
	}
	if !isVscreenToolName("mcp__multica-vscreen__vscreen_list_apps") {
		t.Fatal("inventory tool bypassed transcript filtering")
	}
	found := false
	for _, tool := range vscreenToolDescriptors() {
		if tool["name"] == "vscreen_list_apps" {
			found = true
		}
	}
	if !found {
		t.Fatal("discovery absent from tools/list")
	}
}
