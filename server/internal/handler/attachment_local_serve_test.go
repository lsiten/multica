package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func u(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	return parseUUID(s)
}

func TestSameRuntimeLocalFileRef(t *testing.T) {
	producer := db.AgentTaskQueue{
		RuntimeID:      u("11111111-1111-1111-1111-111111111111"),
		WorkDir:        pgtype.Text{String: "/work/xyz", Valid: true},
		DurableWorkDir: pgtype.Text{String: "/durable/out", Valid: true},
	}
	sameRuntime := producer
	sameRuntime.WorkDir = pgtype.Text{}
	crossRuntime := producer
	crossRuntime.RuntimeID = u("22222222-2222-2222-2222-222222222222")
	noRuntime := producer
	noRuntime.RuntimeID = pgtype.UUID{}

	cases := []struct {
		name     string
		req      db.AgentTaskQueue
		body     string
		wantPath string
	}{
		{
			name:     "same runtime, file url",
			req:      sameRuntime,
			body:     "output at file:///tmp/chart.png done",
			wantPath: "file:///tmp/chart.png",
		},
		{
			name:     "same runtime, work dir returns the file not the prefix",
			req:      producer,
			body:     "saved to /work/xyz/out.png",
			wantPath: "/work/xyz/out.png",
		},
		{
			name:     "same runtime, durable work dir",
			req:      sameRuntime,
			body:     "at /durable/out/file",
			wantPath: "/durable/out/file",
		},
		{
			name:     "cross runtime never serves local",
			req:      crossRuntime,
			body:     "saved to /work/xyz/out.png",
			wantPath: "",
		},
		{
			name:     "requester with no runtime never serves local",
			req:      noRuntime,
			body:     "saved to /work/xyz/out.png",
			wantPath: "",
		},
		{
			name:     "same runtime but no local reference",
			req:      producer,
			body:     "a normal reply with no paths",
			wantPath: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameRuntimeLocalFileRef(c.req, producer, c.body); got != c.wantPath {
				t.Fatalf("sameRuntimeLocalFileRef = %q, want %q", got, c.wantPath)
			}
		})
	}
}

func TestLocalRefForSameRuntime(t *testing.T) {
	// The path is derived from the PRODUCER task's work_dir / durable_work_dir;
	// the requesting task only supplies the runtime to compare against.
	producer := db.AgentTaskQueue{
		RuntimeID:      u("11111111-1111-1111-1111-111111111111"),
		WorkDir:        pgtype.Text{String: "/work/xyz", Valid: true},
		DurableWorkDir: pgtype.Text{String: "/durable/out", Valid: true},
	}
	sameReq := producer // same runtime as producer
	crossReq := producer
	crossReq.RuntimeID = u("22222222-2222-2222-2222-222222222222")
	noRuntimeReq := producer
	noRuntimeReq.RuntimeID = pgtype.UUID{}

	durableOnly := producer
	durableOnly.WorkDir = pgtype.Text{}
	noDir := producer
	noDir.WorkDir = pgtype.Text{}
	noDir.DurableWorkDir = pgtype.Text{}

	cases := []struct {
		name     string
		req      db.AgentTaskQueue
		produced db.AgentTaskQueue
		filename string
		wantPath string
	}{
		{name: "same runtime, work dir wins", req: sameReq, produced: producer, filename: "chart.png", wantPath: "/work/xyz/chart.png"},
		{name: "same runtime, durable work dir when work dir empty", req: sameReq, produced: durableOnly, filename: "out.png", wantPath: "/durable/out/out.png"},
		{name: "cross runtime never serves local", req: crossReq, produced: producer, filename: "chart.png", wantPath: ""},
		{name: "requester with no runtime never serves local", req: noRuntimeReq, produced: producer, filename: "chart.png", wantPath: ""},
		{name: "same runtime but no work dir", req: sameReq, produced: noDir, filename: "chart.png", wantPath: ""},
		{name: "empty filename never serves local", req: sameReq, produced: producer, filename: "", wantPath: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := localRefForSameRuntime(c.req, c.produced, c.filename); got != c.wantPath {
				t.Fatalf("localRefForSameRuntime = %q, want %q", got, c.wantPath)
			}
		})
	}
}
