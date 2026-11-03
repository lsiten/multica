package handler

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sameRuntimeLocalFileRef answers the P1 same-daemon question: can this
// attachment's content be served straight from a local disk the requesting
// agent already shares, instead of round-tripping it through server storage?
//
// It is lexical and best-effort — the server cannot stat the daemon's disk —
// so it only claims "yes" when two things are simultaneously true and
// unambiguous, and returns "" (=> caller serves from server storage exactly as
// before) on any uncertainty:
//
//  1. same runtime: the requesting task and the task that produced the
//     attachment share the same runtime_id. Only then do both tasks live on
//     the same daemon and the same filesystem, so a local read is meaningful
//     at all. A cross-runtime request returns "".
//
//  2. resolvable local path: the body (the producer's reply) names a concrete
//     file — a file:// URL, or the producer's own recorded work_dir /
//     durable_work_dir prefix followed by the file that follows it. The
//     returned value is that file, not merely the directory prefix.
//
// The optimization is therefore a strict fast-path with a guaranteed fallback:
// it can only ever *avoid* the server round-trip, never change what a
// cross-daemon consumer receives.
func sameRuntimeLocalFileRef(requesting, produced db.AgentTaskQueue, body string) string {
	if requesting.RuntimeID == produced.RuntimeID {
		// pgtype.UUID zero-compare is only safe when both are Valid; an
		// empty runtime on either side is not "same runtime".
		if requesting.RuntimeID.Valid && produced.RuntimeID.Valid {
			return localFileFromBody(produced, body)
		}
	}
	return ""
}

// localFileFromBody returns the concrete local file the producer's reply
// references, reusing the classification rules from runtimeLocalPathRef so the
// same-daemon fast path and the completion observer agree. A work_dir /
// durable_work_dir signal returns the matched directory plus the text that
// follows it up to the first path delimiter (the file, not the directory).
func localFileFromBody(produced db.AgentTaskQueue, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if i := strings.Index(strings.ToLower(body), "file://"); i >= 0 {
		return extractFileURL(body, i)
	}
	for _, dir := range []string{produced.WorkDir.String, produced.DurableWorkDir.String} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if i := strings.Index(body, dir); i >= 0 {
			return extractFileURL(body, i)
		}
	}
	return ""
}

// extractFileURL returns the file:// URL (or work_dir match) that begins at
// start, up to the first delimiter that ends a bare URL/path in prose:
// whitespace, a quote, or a closing bracket. It is best effort and never
// panics on a trailing entry with no delimiter.
func extractFileURL(body string, start int) string {
	for j := start; j < len(body); j++ {
		switch body[j] {
		case ' ', '\t', '\n', '\r', '"', ')', ']', '}':
			return body[start:j]
		}
	}
	return body[start:]
}

// localRefForAttachment is the P1 same-runtime fast-path decision, consumed by
// the single-attachment response. It returns a local file path a same-runtime
// consumer can read straight from its own daemon's disk instead of
// round-tripping the attachment through server storage.
//
// Unlike sameRuntimeLocalFileRef (which reads the producer's reply body, only
// available to the chat-message observer) this is body-independent: the
// response path has no reply body, so it derives the path from the producer's
// recorded work_dir / durable_work_dir plus the attachment's filename. It is a
// best-effort hint — the consumer attempts a local read of the returned path
// and, on any miss (the file may live under a subdirectory the work_dir alone
// cannot name), falls back to the server download endpoint, which is left
// exactly as it was. So the hint can only ever *avoid* a server round-trip,
// never change what a cross-daemon consumer receives. An empty result is
// "no local reference" and the caller simply omits the field.
func (h *Handler) localRefForAttachment(ctx context.Context, requestingTaskID pgtype.UUID, att db.Attachment) string {
	if !att.TaskID.Valid {
		return ""
	}
	if !requestingTaskID.Valid {
		// Only an agent requester (carries X-Task-ID) can consume a local
		// fast-path; a member/browser requester has no shared daemon disk.
		return ""
	}
	produced, err := h.Queries.GetAgentTask(ctx, att.TaskID)
	if err != nil {
		return ""
	}
	requesting, err := h.Queries.GetAgentTask(ctx, requestingTaskID)
	if err != nil {
		return ""
	}
	return localRefForSameRuntime(requesting, produced, att.Filename)
}

// localRefForSameRuntime returns the concrete local file a same-runtime
// consumer can read, deriving it from the producer's recorded work_dir /
// durable_work_dir plus the attachment's filename. It returns "" — the caller
// serves from server storage exactly as before — on any uncertainty: a
// cross-runtime request, a runtime-less request, or a producer without a
// resolvable work directory. work_dir (the transient, current-turn directory)
// wins over durable_work_dir; durable_work_dir is used only when the producer
// has no work_dir.
func localRefForSameRuntime(requesting, produced db.AgentTaskQueue, filename string) string {
	if !requesting.RuntimeID.Valid || !produced.RuntimeID.Valid {
		return ""
	}
	if requesting.RuntimeID != produced.RuntimeID {
		return ""
	}
	if strings.TrimSpace(filename) == "" {
		return ""
	}
	for _, dir := range []string{produced.WorkDir.String, produced.DurableWorkDir.String} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		return filepath.Join(dir, filename)
	}
	return ""
}
