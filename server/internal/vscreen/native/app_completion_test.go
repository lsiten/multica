package native

import "testing"

func TestPIDCompletionHasNoPrivateParentRPCSwitch(t *testing.T) {
	h, a, _ := appFixture(t)
	if err := h.grant(appReq("app_grant", a)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execute(t.Context(), appReq("app_resume", a)); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"pid_complete", "app_pid_complete", "app_complete"} {
		if _, err := h.execute(t.Context(), appReq(op, a)); err == nil {
			t.Fatalf("parent could clear completion using %s", op)
		}
	}
}
