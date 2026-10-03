package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const jevLogPayloadPartBytes = 32 << 10

type jevDecisionLogContextKey struct{}

type jevDecisionLog struct {
	logger  *slog.Logger
	secrets []string
	started time.Time
	attempt int
	taskID  string
	record  protocol.JevDecisionLog
	report  jevDecisionReportFunc
}

type jevDecisionReportFunc func(string, protocol.JevDecisionLog) error
type jevDecisionReporterContextKey struct{}

func withJevDecisionReporter(ctx context.Context, report jevDecisionReportFunc) context.Context {
	return context.WithValue(ctx, jevDecisionReporterContextKey{}, report)
}

func (s *llm2jevMCPServer) configureDecisionLogging(task Task, provider string) {
	s.decisionLogSecrets = []string{task.AuthToken, task.RemoteMCPDaemonToken, s.apiKey, s.endpoint, strings.TrimPrefix(s.path, "/")}
	for key, value := range task.Agent.CustomEnv {
		if jevLogSecretKey(key) || key == "OPENAI_BASE_URL" || key == "OPENAI_API_BASE" {
			s.decisionLogSecrets = append(s.decisionLogSecrets, value)
		}
	}
	source, modelRevision, device, configRevision := "agent_context", "", "", int64(0)
	if s.systemOne {
		source = "system_one"
	}
	if task.JevConfig != nil {
		source, modelRevision, configRevision = task.JevConfig.Source, task.JevConfig.ModelRevision, task.JevConfig.Revision
		device = task.JevConfig.Device
		s.decisionLogSecrets = append(s.decisionLogSecrets, task.JevConfig.Endpoint)
	}
	sort.Slice(s.decisionLogSecrets, func(i, j int) bool { return len(s.decisionLogSecrets[i]) > len(s.decisionLogSecrets[j]) })
	agentID := task.AgentID
	if agentID == "" {
		agentID = task.Agent.ID
	}
	s.decisionLogAttrs = []any{
		"task_id", s.taskID, "issue_id", task.IssueID, "issue_identifier", task.IssueIdentifier,
		"workspace_id", task.WorkspaceID, "project_id", task.ProjectID, "runtime_id", task.RuntimeID,
		"agent_id", agentID, "agent_name", task.Agent.Name, "chat_session_id", task.ChatSessionID,
		"source", source, "provider", provider, "model", s.model,
		"model_revision", modelRevision, "device", device, "config_revision", configRevision, "timeout_ms", s.callTimeout.Milliseconds(),
	}
	for index := 1; index < len(s.decisionLogAttrs); index += 2 {
		if value, ok := s.decisionLogAttrs[index].(string); ok {
			s.decisionLogAttrs[index] = redactJevLogText(value, s.decisionLogSecrets)
		}
	}
	s.decisionLogRecord = protocol.JevDecisionLog{Source: source, Model: redactJevLogText(s.model, s.decisionLogSecrets), ModelRevision: redactJevLogText(modelRevision, s.decisionLogSecrets), ConfigRevision: configRevision, Device: device}
}

func (s *llm2jevMCPServer) startDecisionLog(tool string, input json.RawMessage) *jevDecisionLog {
	decision := &jevDecisionLog{secrets: s.decisionLogSecrets, started: time.Now(), taskID: s.taskID, record: s.decisionLogRecord, report: s.decisionReporter}
	decision.record.ID = uuid.NewString()
	decision.record.Tool = tool
	decision.record.StartedAt = decision.started.UTC()
	decision.record.ResultClass = "running"
	decision.record.Input = jevLogPayload(input, decision.secrets)
	decision.record.Requests = []protocol.JevDecisionRequestLog{}
	decision.persist()
	if s.logger == nil {
		return decision
	}
	decision.logger = s.logger.With(s.decisionLogAttrs...).With("decision_id", decision.record.ID, "tool", tool)
	decision.logger.Info("jev decision started", decision.payloadAttrs(decision.logger, "started", "input", input)...)
	return decision
}

func (d *jevDecisionLog) complete(output map[string]any, resultClass, errorCode string) {
	raw, err := json.Marshal(output)
	if err != nil {
		if d.logger != nil {
			d.logger.Error("jev decision log encoding failed", "error", err)
		}
		return
	}
	completed := time.Now().UTC()
	d.record.CompletedAt = &completed
	d.record.DurationMS = time.Since(d.started).Milliseconds()
	d.record.ResultClass, d.record.ErrorCode = resultClass, errorCode
	d.record.Output = jevLogPayload(raw, d.secrets)
	d.persist()
	if d.logger == nil {
		return
	}
	attrs := []any{"result_class", resultClass, "error_code", errorCode, "duration_ms", time.Since(d.started).Milliseconds(), "provider_attempts", d.attempt}
	attrs = append(attrs, d.payloadAttrs(d.logger, "completed", "output", raw)...)
	d.logger.Info("jev decision completed", attrs...)
}

func (d *jevDecisionLog) modelRequest(variant string, status int, resultClass string, duration time.Duration, input, output []byte) {
	d.attempt++
	d.record.Requests = append(d.record.Requests, protocol.JevDecisionRequestLog{Attempt: d.attempt, Variant: variant, HTTPStatus: status, ResultClass: resultClass, DurationMS: duration.Milliseconds(), Input: jevLogPayload(input, d.secrets), Output: jevLogPayload(output, d.secrets), ResponseIncomplete: resultClass == "response_too_large"})
	if d.logger == nil {
		return
	}
	logger := d.logger.With("provider_attempt", d.attempt)
	attrs := []any{"variant", variant, "http_status", status, "result_class", resultClass, "duration_ms", duration.Milliseconds(), "response_incomplete", resultClass == "response_too_large"}
	attrs = append(attrs, d.payloadAttrs(logger, "model_request", "input", input)...)
	attrs = append(attrs, d.payloadAttrs(logger, "model_request", "output", output)...)
	logger.Info("llm2jev model request", attrs...)
}

func (d *jevDecisionLog) persist() {
	if d.report == nil {
		return
	}
	if err := d.report(d.taskID, d.record); err != nil {
		logger := d.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("persist Jev decision report failed", "decision_id", d.record.ID, "task_id", d.taskID, "error", err)
	}
}

// Large payloads are split without dropping content or exceeding daemon.log's
// rotating writer limit. Each part keeps its decision and provider-attempt IDs.
func (d *jevDecisionLog) payloadAttrs(logger *slog.Logger, stage, field string, raw []byte) []any {
	payload := jevLogPayload(raw, d.secrets)
	if len(payload) <= jevLogPayloadPartBytes {
		return []any{field, payload}
	}
	var parts []string
	for start := 0; start < len(payload); {
		end := min(start+jevLogPayloadPartBytes, len(payload))
		for end < len(payload) && !utf8.RuneStart(payload[end]) {
			end--
		}
		parts = append(parts, payload[start:end])
		start = end
	}
	for index, part := range parts {
		logger.Info("jev decision payload", "stage", stage, "field", field, "part", index+1, "parts", len(parts), "payload", part)
	}
	return []any{field, "[chunked]", field + "_parts", len(parts), field + "_bytes", len(payload)}
}

func jevDecisionLogFromContext(ctx context.Context) *jevDecisionLog {
	decision, _ := ctx.Value(jevDecisionLogContextKey{}).(*jevDecisionLog)
	return decision
}

func jevLogSecretKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return normalized == "authorization" || normalized == "cookie" || normalized == "setcookie" || normalized == "credentials" ||
		strings.HasSuffix(normalized, "apikey") || strings.HasSuffix(normalized, "token") ||
		strings.HasSuffix(normalized, "password") || strings.HasSuffix(normalized, "secret") || strings.HasSuffix(normalized, "credential")
}

func redactJevLogText(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}

func jevLogPayload(raw []byte, secrets []string) string {
	if len(raw) == 0 {
		return "null"
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF {
		return redactJevLogText(string(raw), secrets)
	}
	clean, err := json.Marshal(redactJevLogValue(value, secrets))
	if err != nil {
		return fmt.Sprintf("[payload encoding failed: %v]", err)
	}
	return string(clean)
}

func redactJevLogValue(value any, secrets []string) any {
	switch value := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(value))
		for key, entry := range value {
			cleanKey := redactJevLogText(key, secrets)
			if jevLogSecretKey(key) {
				clean[cleanKey] = "[redacted]"
			} else {
				clean[cleanKey] = redactJevLogValue(entry, secrets)
			}
		}
		return clean
	case []any:
		for index, entry := range value {
			value[index] = redactJevLogValue(entry, secrets)
		}
	case string:
		// Model replies and semantic prompts can contain JSON encoded as text.
		// Redact credential fields inside that JSON as well as known secrets.
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return jevLogPayload([]byte(value), secrets)
		}
		return redactJevLogText(value, secrets)
	}
	return value
}
