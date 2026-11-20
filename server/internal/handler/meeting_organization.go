package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/llm"
)

type meetingOrganization struct {
	Title       string   `json:"title"`
	Topic       string   `json:"topic"`
	Summary     string   `json:"summary"`
	Decisions   []string `json:"decisions"`
	ActionItems []string `json:"action_items"`
	ProjectHint string   `json:"project_hint"`
}

func organizeMeeting(ctx context.Context, client *llm.Client, transcript string, startedAt time.Time) meetingOrganization {
	fallback := meetingOrganization{
		Title:       fallbackMeetingTitle(transcript, startedAt),
		Topic:       detectMeetingTopic(transcript),
		Summary:     fallbackMeetingSummary(transcript),
		Decisions:   []string{},
		ActionItems: []string{},
	}
	if client == nil || !client.Enabled() || strings.TrimSpace(transcript) == "" {
		return fallback
	}
	input := truncateMeetingText(transcript, 12_000)
	prompt := "Return JSON only. JSON schema: {title:string,topic:string,summary:string,decisions:string[],action_items:string[],project_hint:string}. " +
		"Create a concise Chinese meeting record from this transcript. Do not invent facts, owners, dates, or projects. " +
		"Use an empty string or empty array when the transcript does not support a value.\n\nTRANSCRIPT:\n" + input
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	raw, err := client.GenerateJSON(callCtx, "", "You organize meeting transcripts. Always answer with valid JSON.", prompt, 0, 1200)
	if err != nil {
		return fallback
	}
	var parsed meetingOrganization
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return fallback
	}
	parsed.Title = limitMeetingText(parsed.Title, 200)
	parsed.Topic = limitMeetingText(parsed.Topic, 120)
	parsed.Summary = limitMeetingText(parsed.Summary, 4_000)
	parsed.ProjectHint = limitMeetingText(parsed.ProjectHint, 200)
	if parsed.Title == "" {
		parsed.Title = fallback.Title
	}
	if parsed.Topic == "" {
		parsed.Topic = fallback.Topic
	}
	if parsed.Summary == "" {
		parsed.Summary = fallback.Summary
	}
	if parsed.Decisions == nil {
		parsed.Decisions = []string{}
	}
	if parsed.ActionItems == nil {
		parsed.ActionItems = []string{}
	}
	return parsed
}

func chooseMeetingProject(projects []db.Project, text string) (db.Project, bool) {
	needle := strings.ToLower(text)
	var best db.Project
	bestLength := 0
	for _, project := range projects {
		title := strings.TrimSpace(project.Title)
		if len([]rune(title)) < 2 || !strings.Contains(needle, strings.ToLower(title)) {
			continue
		}
		if len([]rune(title)) > bestLength {
			best = project
			bestLength = len([]rune(title))
		}
	}
	return best, bestLength > 0
}

func fallbackMeetingTitle(transcript string, startedAt time.Time) string {
	text := normalizeMeetingOpening(transcript)
	if text != "" {
		for _, separator := range []string{"。", "！", "？", ".", "!", "?", "\n"} {
			if index := strings.Index(text, separator); index >= 4 {
				return limitMeetingText(strings.TrimSpace(text[:index]), 80)
			}
		}
		return limitMeetingText(text, 80)
	}
	return "会议记录 " + startedAt.Format("2006-01-02 15:04")
}

func fallbackMeetingSummary(transcript string) string {
	if strings.TrimSpace(transcript) == "" {
		return ""
	}
	return "会议已完成转写，暂未生成结构化摘要。"
}

func normalizeMeetingOpening(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end >= 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	if strings.HasPrefix(text, "(") {
		if end := strings.Index(text, ")"); end >= 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	return text
}

func detectMeetingTopic(transcript string) string {
	text := strings.ToLower(transcript)
	for _, candidate := range []struct {
		name     string
		keywords []string
	}{
		{name: "发布计划", keywords: []string{"发布", "上线", "release", "deploy"}},
		{name: "客户反馈", keywords: []string{"客户", "反馈", "用户", "customer", "feedback"}},
		{name: "技术讨论", keywords: []string{"技术", "架构", "接口", "代码", "technical", "architecture"}},
		{name: "项目计划", keywords: []string{"项目", "计划", "里程碑", "project", "milestone"}},
		{name: "运营与业务", keywords: []string{"运营", "业务", "销售", "市场", "business", "marketing"}},
	} {
		for _, keyword := range candidate.keywords {
			if strings.Contains(text, keyword) {
				return candidate.name
			}
		}
	}
	return "未分类"
}

func truncateMeetingText(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}

func limitMeetingText(text string, max int) string {
	return strings.TrimSpace(truncateMeetingText(text, max))
}
