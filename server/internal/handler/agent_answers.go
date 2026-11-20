package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ListAgentAnswers reads only answer/error text, bounded both at the database
// projection and wire boundary. Long answers continue at a character offset;
// advancing the cursor never silently discards the rest of an answer.
func (h *Handler) ListAgentAnswers(w http.ResponseWriter, r *http.Request) {
	task, ok := h.loadTaskForUser(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("view") == "final" {
		h.listFinalAgentAnswer(w, r, task)
		return
	}
	seq, offset := int64(0), int64(0)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		parts := strings.Split(raw, ":")
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "invalid answer cursor")
			return
		}
		var seqErr, offsetErr error
		seq, seqErr = strconv.ParseInt(parts[0], 10, 33)
		offset, offsetErr = strconv.ParseInt(parts[1], 10, 32)
		if seqErr != nil || offsetErr != nil || seq < 0 || seq > 1<<31 || offset < 0 || offset >= 1<<31-1 {
			writeError(w, http.StatusBadRequest, "invalid answer cursor")
			return
		}
	}
	limit := int64(20)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || limit < 1 || limit > 50 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 50")
			return
		}
	}
	rows, err := h.Queries.ListAgentAnswerChunks(r.Context(), db.ListAgentAnswerChunksParams{
		TaskID: task.ID, CursorSeq: seq, ContentOffset: int32(offset), PageLimit: int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read agent answers")
		return
	}
	if offset > 0 && (len(rows) == 0 || int64(rows[0].Seq) != seq || offset >= int64(rows[0].TotalChars)) {
		writeError(w, http.StatusBadRequest, "answer cursor is outside the message")
		return
	}
	page := protocol.AgentAnswerPage{TaskID: uuidToString(task.ID), RunStatus: task.Status, Messages: []protocol.AgentAnswerChunk{}, NextCursor: strconv.FormatInt(seq, 10) + ":" + strconv.FormatInt(offset, 10)}
	remaining := 48 << 10
	for i, row := range rows {
		if i == int(limit) {
			page.HasMore = true
			break
		}
		start := int32(0)
		if int64(row.Seq) == seq {
			start = int32(offset)
		}
		end := start + int32(utf8.RuneCountInString(row.Content))
		chunk := protocol.AgentAnswerChunk{ID: uuidToString(row.ID), Seq: row.Seq, Type: row.Type, Content: row.Content, Offset: start, HasMoreContent: end < row.TotalChars, CreatedAt: timestampToString(row.CreatedAt)}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode agent answer")
			return
		}
		if len(encoded) > remaining {
			page.HasMore = true
			break
		}
		remaining -= len(encoded) + 1
		page.Messages = append(page.Messages, chunk)
		if chunk.HasMoreContent {
			page.NextCursor = strconv.FormatInt(int64(row.Seq), 10) + ":" + strconv.FormatInt(int64(end), 10)
			page.HasMore = true
			break
		}
		page.NextCursor = strconv.FormatInt(int64(row.Seq)+1, 10) + ":0"
	}
	writeJSON(w, http.StatusOK, page)
}

// listFinalAgentAnswer reads the authoritative terminal payload independently
// of task_message delivery. A worker may have committed the Run's terminal
// result while its last transcript batch is still queued locally. Keeping this
// view explicit prevents a late transcript from changing the normal cursor
// contract and makes the distinction visible to callers.
func (h *Handler) listFinalAgentAnswer(w http.ResponseWriter, r *http.Request, task db.AgentTaskQueue) {
	cursor := r.URL.Query().Get("cursor")
	offset := int64(0)
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || parts[0] != "final" {
			writeError(w, http.StatusBadRequest, "final answer cursor is invalid")
			return
		}
		parsed, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil || parsed < 0 || parsed >= 1<<31-1 {
			writeError(w, http.StatusBadRequest, "final answer cursor is invalid")
			return
		}
		offset = parsed
	}
	page := protocol.AgentAnswerPage{TaskID: uuidToString(task.ID), RunStatus: task.Status, Messages: []protocol.AgentAnswerChunk{}, NextCursor: "final:" + strconv.FormatInt(offset, 10)}
	if task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled" {
		writeJSON(w, http.StatusOK, page)
		return
	}
	content, kind := terminalAnswerContent(task)
	total := int64(utf8.RuneCountInString(content))
	if offset > total {
		writeError(w, http.StatusBadRequest, "final answer cursor is outside the result")
		return
	}
	if offset == total || content == "" {
		writeJSON(w, http.StatusOK, page)
		return
	}
	remaining := []rune(content)[offset:]
	if len(remaining) > 4096 {
		remaining = remaining[:4096]
	}
	chunk := protocol.AgentAnswerChunk{
		ID: uuidToString(task.ID), Seq: 0, Type: kind, Content: string(remaining), Offset: int32(offset),
		HasMoreContent: offset+int64(len(remaining)) < total, CreatedAt: timestampToString(task.CompletedAt),
	}
	page.Messages = append(page.Messages, chunk)
	page.HasMore = chunk.HasMoreContent
	page.NextCursor = "final:" + strconv.FormatInt(offset+int64(len(remaining)), 10)
	writeJSON(w, http.StatusOK, page)
}

func terminalAnswerContent(task db.AgentTaskQueue) (string, string) {
	if len(task.Result) > 0 {
		var result struct {
			Output string `json:"output"`
		}
		if json.Unmarshal(task.Result, &result) == nil && result.Output != "" {
			return result.Output, "final"
		}
	}
	if task.Error.Valid && task.Error.String != "" {
		return task.Error.String, "error"
	}
	return "", "final"
}
