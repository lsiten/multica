package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// TestRuntimeLocalPathRef pins the classification + concrete path the P1
// same-daemon fast path will consume. The path must be the concrete file (not
// just the directory prefix) so a same-daemon consumer can fetch it; the metric
// kind stays stable so the completion observer and the fast path agree. It also
// covers the daemon-confirmed durable_work_dir, which the observation-only
// predecessor never returned.
func TestRuntimeLocalPathRef(t *testing.T) {
	cases := []struct {
		name     string
		task     db.AgentTaskQueue
		body     string
		wantKind string
		wantPath string
	}{
		{
			name:     "file url in prose",
			body:     "output at file:///tmp/chart.png done",
			wantKind: "file_url",
			wantPath: "file:///tmp/chart.png",
		},
		{
			name:     "work dir returns the full file path, not just the prefix",
			task:     db.AgentTaskQueue{WorkDir: text("/work/xyz")},
			body:     "saved to /work/xyz/out.png",
			wantKind: "workdir_path",
			wantPath: "/work/xyz/out.png",
		},
		{
			name:     "durable work dir when work dir empty",
			task:     db.AgentTaskQueue{DurableWorkDir: text("/durable/out")},
			body:     "at /durable/out/file",
			wantKind: "workdir_path",
			wantPath: "/durable/out/file",
		},
		{
			name:     "work dir wins over durable when both present",
			task:     db.AgentTaskQueue{WorkDir: text("/work"), DurableWorkDir: text("/durable")},
			body:     "/durable/x and /work/y",
			wantKind: "workdir_path",
			wantPath: "/work/y",
		},
		{
			name: "no local reference",
			task: db.AgentTaskQueue{WorkDir: text("/work"), DurableWorkDir: text("/durable")},
			body: "just a normal reply with no paths",
		},
		{
			name: "empty body",
			body: "   ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, path := runtimeLocalPathRef(c.task, c.body)
			if kind != c.wantKind || path != c.wantPath {
				t.Fatalf("runtimeLocalPathRef = (%q, %q), want (%q, %q)", kind, path, c.wantKind, c.wantPath)
			}
		})
	}
}

// TestExtractFileURL verifies the best-effort boundary: a bare URL/path stops at
// the first whitespace, quote, or closing bracket, and a trailing entry with no
// delimiter returns the whole remainder without panicking.
func TestExtractFileURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{`file://a b`, `file://a`},
		{`file://a) b`, `file://a`},
		{`file://a", b`, `file://a`},
		{`file://a}`, `file://a`},
		{`file:///tmp/x.png`, `file:///tmp/x.png`},
	}
	for _, c := range cases {
		if got := extractFileURL(c.in, 0); got != c.want {
			t.Fatalf("extractFileURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
