package daemon

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"testing"
)

func TestMirrorCLIDecisionDoesNotRequestSystemPermissions(t *testing.T) {
	d := &Daemon{}
	err := d.handleMirrorAuthorization(t.Context(), protocol.MirrorAuthorizationRequest{Kind: "cli"}, true)
	if err == nil {
		t.Fatal("CLI request accepted by system permission handler")
	}
	if d.vscreen != nil {
		t.Fatal("CLI approval accessed the native screen host")
	}
}

func TestMirrorDeniedSystemDecisionDoesNotStartNativeHost(t *testing.T) {
	d := &Daemon{}
	err := d.handleMirrorAuthorization(t.Context(), protocol.MirrorAuthorizationRequest{Kind: "system"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.vscreen != nil {
		t.Fatal("denial accessed the native screen host")
	}
}
