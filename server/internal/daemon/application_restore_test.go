package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationRestoreCannotBypassPendingOrchestration(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "pending start"
		if approved {
			name = "previously completed start"
		}
		t.Run(name, func(t *testing.T) {
			var command protocol.ApplicationControlCommand
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/sync"):
					if err := json.NewEncoder(w).Encode([]protocol.ApplicationRuntimeInstance{{DesiredState: "running", CanRestore: approved, Command: command}}); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(r.URL.Path, "/claim"):
					if err := json.NewEncoder(w).Encode([]protocol.ApplicationClaim{}); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(r.URL.Path, "/observe"):
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			d, configured := applicationManagerFixture(t, server.URL)
			command = configured
			command.Config.Restart.Restore = true
			var launches atomic.Int64
			d.applicationHostLauncher = func(string) (*exec.Cmd, error) {
				launches.Add(1)
				return nil, errors.New("test host launch deliberately unavailable")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var workers sync.WaitGroup
			d.pollRuntimeApplications(ctx, make(chan struct{}, 4), &workers, command.RuntimeID)
			workers.Wait()
			want := int64(0)
			if approved {
				want = 1
			}
			if launches.Load() != want {
				t.Fatalf("restore launches=%d want=%d", launches.Load(), want)
			}
		})
	}
}
