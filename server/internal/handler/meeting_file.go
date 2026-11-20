package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) GetMeetingFile(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if meeting.Status != "ready" {
		writeError(w, http.StatusConflict, "meeting is not ready")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="meeting-record.md"`)
	_, _ = w.Write([]byte(meetingMarkdown(meeting)))
}

func meetingMarkdown(meeting db.MeetingSession) string {
	var decisions, actionItems []string
	if err := json.Unmarshal(meeting.Decisions, &decisions); err != nil {
		decisions = []string{}
	}
	if err := json.Unmarshal(meeting.ActionItems, &actionItems); err != nil {
		actionItems = []string{}
	}
	var builder strings.Builder
	title := meeting.Title
	if title == "" {
		title = "会议记录"
	}
	fmt.Fprintf(&builder, "# %s\n\n", title)
	fmt.Fprintf(&builder, "- 日期：%s\n- 时区：%s\n- 运行时：%s\n- 主题：%s\n", meeting.StartedAt.Time.Format("2006-01-02 15:04"), meeting.Timezone, uuidToOptionalString(meeting.RuntimeID), meeting.Topic)
	if meeting.ProjectID.Valid {
		fmt.Fprintf(&builder, "- 项目：%s\n", uuidToString(meeting.ProjectID))
	}
	fmt.Fprintf(&builder, "\n## 摘要\n\n%s\n\n## 决策\n\n", meeting.Summary)
	for _, decision := range decisions {
		fmt.Fprintf(&builder, "- %s\n", decision)
	}
	builder.WriteString("\n## 行动项\n\n")
	for _, action := range actionItems {
		fmt.Fprintf(&builder, "- %s\n", action)
	}
	fmt.Fprintf(&builder, "\n## 原始转写\n\n%s\n", meeting.Transcript)
	return builder.String()
}
