package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDetectMeetingTopic(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "release", text: "我们讨论本周发布和上线。", want: "发布计划"},
		{name: "customer", text: "客户反馈需要跟进。", want: "客户反馈"},
		{name: "fallback", text: "闲聊。", want: "未分类"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectMeetingTopic(tt.text); got != tt.want {
				t.Fatalf("detectMeetingTopic(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestMeetingMarkdownIncludesStructuredSections(t *testing.T) {
	markdown := meetingMarkdown(db.MeetingSession{
		Title: "发布计划", Timezone: "Asia/Shanghai", Topic: "发布",
		StartedAt: pgtype.Timestamptz{Time: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), Valid: true},
		Summary:   "讨论发布。", Transcript: "[00:00] 发言人：发布。",
		Decisions: []byte(mustJSON(t, []string{"周五发布"})), ActionItems: []byte(mustJSON(t, []string{"准备说明"})),
	})
	for _, want := range []string{"# 发布计划", "## 摘要", "周五发布", "准备说明", "## 原始转写"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("meeting markdown missing %q: %s", want, markdown)
		}
	}
}

func TestFallbackMeetingTitle(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	if got := fallbackMeetingTitle("[00:00] ( 字幕:J Chong ) 本周发布。需要准备说明。", started); got != "本周发布" {
		t.Fatalf("fallbackMeetingTitle(text) = %q, want %q", got, "本周发布")
	}
	if got := fallbackMeetingTitle("", started); got != "会议记录 2026-11-20 10:00" {
		t.Fatalf("fallbackMeetingTitle(empty) = %q, want %q", got, "会议记录 2026-11-20 10:00")
	}
}

func TestFallbackMeetingSummaryDoesNotDuplicateTranscript(t *testing.T) {
	if got := fallbackMeetingSummary("[00:00] 发言内容"); !strings.Contains(got, "结构化摘要") {
		t.Fatalf("fallbackMeetingSummary() = %q", got)
	}
}
