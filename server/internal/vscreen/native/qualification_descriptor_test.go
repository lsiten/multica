//go:build darwin || linux

package native

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
)

func TestQualificationInheritedDescriptor(t *testing.T) {
	if os.Getenv("MULTICA_TEST_QUALIFICATION_FD") == "1" {
		input := os.NewFile(7, "owned-qualification")
		defer input.Close()
		scope, err := readQualificationDescriptor(input, 200*time.Millisecond)
		if err != nil || scope.Nonce != "owned-fixture" {
			os.Exit(21)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"valid", "malformed", "oversize", "regular", "stalled"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			input, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer writer.Close()
			placeholder, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer placeholder.Close()
			inherited := input
			if mode == "regular" {
				inherited = placeholder
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestQualificationInheritedDescriptor$")
			cmd.Env = append(os.Environ(), "MULTICA_TEST_QUALIFICATION_FD=1", "GORACE=atexit_sleep_ms=0")
			cmd.ExtraFiles = []*os.File{placeholder, placeholder, placeholder, placeholder, inherited}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			input.Close()
			raw, _ := json.Marshal(smokefixture.QualificationScope{Nonce: "owned-fixture"})
			if mode == "malformed" {
				raw = []byte("{")
			}
			if mode == "oversize" {
				raw = []byte(strings.Repeat("x", 4097))
			}
			if mode != "stalled" && mode != "regular" {
				_, _ = writer.Write(raw)
				writer.Close()
			}
			err = cmd.Wait()
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}
