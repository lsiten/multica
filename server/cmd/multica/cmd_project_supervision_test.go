package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

func TestProjectSupervisionCLIContracts(t *testing.T) {
	const project = "11111111-1111-4111-8111-111111111111"
	for _, op := range []string{"get", "configure", "check", "apply", "report"} {
		t.Run(op, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/projects/"+project {
					json.NewEncoder(w).Encode(map[string]string{"id": project, "title": "Project"})
					return
				}
				path := "/api/projects/" + project + "/supervision"
				method := http.MethodGet
				switch op {
				case "configure":
					method = http.MethodPut
				case "check":
					path += "/check"
					method = http.MethodPost
				case "apply":
					path += "/actions"
					method = http.MethodPost
				case "report":
					path += "/report"
					method = http.MethodPost
				}
				if r.Method != method || r.URL.Path != path {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if op == "apply" || op == "report" || op == "configure" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["checked_version"] != float64(9) {
						t.Errorf("JSON contract: %v %v", body, err)
					}
				}
				requests++
				json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
			}))
			defer server.Close()
			setCLITestServerEnv(t, server.URL)
			t.Setenv("MULTICA_TOKEN", "mat_coordination-test")
			group := projectSupervisionCommand()
			var command *cobra.Command
			for _, child := range group.Commands() {
				if child.Name() == op {
					command = child
					break
				}
			}
			if command == nil {
				t.Fatal("missing command")
			}
			if op == "configure" || op == "apply" || op == "report" {
				if err := command.Flags().Set("body", `{"checked_version":9}`); err != nil {
					t.Fatal(err)
				}
			}
			if err := command.RunE(command, []string{project}); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("mutation requested %d times", requests)
			}
		})
	}
}
func TestProjectSupervisionCLIRejectsAmbiguousJSON(t *testing.T) {
	group := projectSupervisionCommand()
	var cmd *cobra.Command
	for _, child := range group.Commands() {
		if child.Name() == "apply" {
			cmd = child
		}
	}
	if _, err := supervisionBody(cmd); err == nil {
		t.Fatal("missing body accepted")
	}
	cmd.Flags().Set("body", "[]")
	if _, err := supervisionBody(cmd); err == nil {
		t.Fatal("array accepted")
	}
	cmd.Flags().Set("body", "{}")
	cmd.Flags().Set("body-file", "actions.json")
	if _, err := supervisionBody(cmd); err == nil {
		t.Fatal("ambiguous input accepted")
	}
}
