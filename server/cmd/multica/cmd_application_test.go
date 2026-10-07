package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func applicationTestCommand(t *testing.T, action string) *cobra.Command {
	t.Helper()
	for _, cmd := range applicationCommand().Commands() {
		if cmd.Name() == action {
			return cmd
		}
	}
	t.Fatalf("application command %s missing", action)
	return nil
}

func TestApplicationCLICatalogContracts(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, action := range []string{"list", "get", "create", "update", "delete", "plan"} {
		t.Run(action, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				path, method := "/api/applications/"+id, http.MethodGet
				if action == "list" || action == "create" {
					path = "/api/applications"
				}
				switch action {
				case "create":
					method = http.MethodPost
				case "update":
					method = http.MethodPatch
				case "delete":
					method = http.MethodDelete
				case "plan":
					path += "/plan"
				}
				if r.URL.Path != path || r.Method != method {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				if action == "create" || action == "update" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["revision"] != float64(7) {
						t.Errorf("body=%v err=%v", body, err)
					}
				}
				if action == "delete" {
					if r.URL.Query().Get("revision") != "7" {
						t.Errorf("missing revision: %s", r.URL)
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if _, err := io.WriteString(w, `{"id":"`+id+`"}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			setCLITestServerEnv(t, server.URL)
			t.Setenv("MULTICA_TOKEN", "mat_application_test")
			cmd := applicationTestCommand(t, action)
			var output bytes.Buffer
			cmd.SetOut(&output)
			args := []string{id}
			if action == "list" || action == "create" {
				args = nil
			}
			if action == "create" || action == "update" {
				if err := cmd.Flags().Set("body", `{"revision":7}`); err != nil {
					t.Fatal(err)
				}
			}
			if action == "delete" {
				if err := cmd.Flags().Set("revision", "7"); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.RunE(cmd, args); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || !json.Valid(output.Bytes()) {
				t.Fatalf("requests=%d output=%s", requests, output.String())
			}
		})
	}
}

func TestApplicationCLIRejectsAmbiguousAndOversizedBodies(t *testing.T) {
	for _, input := range []string{"[]", "null", strings.Repeat(" ", 65537)} {
		cmd := applicationTestCommand(t, "create")
		if err := cmd.Flags().Set("body", input); err != nil {
			t.Fatal(err)
		}
		if _, err := applicationBody(cmd); err == nil {
			t.Fatal("invalid body accepted")
		}
	}
	cmd := applicationTestCommand(t, "create")
	if err := cmd.Flags().Set("body-file", "-"); err != nil {
		t.Fatal(err)
	}
	cmd.SetIn(strings.NewReader(`{"name":"preview"}`))
	if body, err := applicationBody(cmd); err != nil || body["name"] != "preview" {
		t.Fatalf("stdin: %v %v", body, err)
	}
	if err := cmd.Flags().Set("body", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := applicationBody(cmd); err == nil {
		t.Fatal("ambiguous body accepted")
	}
}
