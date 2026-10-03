package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const ProjectSupervisionContextType = "project_supervision"

var ErrProjectSupervisionConflict = errors.New("project supervision changed; reload and retry")
var ErrProjectSupervisionForbidden = errors.New("project supervision authority is unavailable")
var ErrProjectExecutionCapacity = errors.New("project execution capacity is full")

type ProjectSupervisionConfig struct {
	AutoAdvance         bool     `json:"auto_advance"`
	MaxInFlight         int      `json:"max_in_flight"`
	BatchSize           int      `json:"batch_size"`
	ScanIntervalSeconds int      `json:"scan_interval_seconds"`
	StaleAfterSeconds   int      `json:"stale_after_seconds"`
	NoProgressLimit     int      `json:"no_progress_limit"`
	ReadyStatuses       []string `json:"ready_statuses"`
}

func DefaultProjectSupervisionConfig() ProjectSupervisionConfig {
	return ProjectSupervisionConfig{MaxInFlight: 3, BatchSize: 3, ScanIntervalSeconds: 300, StaleAfterSeconds: 900, NoProgressLimit: 3, ReadyStatuses: []string{"todo"}}
}
func (c ProjectSupervisionConfig) Validate() error {
	if c.MaxInFlight < 1 || c.MaxInFlight > 100 || c.BatchSize < 1 || c.BatchSize > c.MaxInFlight || c.ScanIntervalSeconds < 60 || c.ScanIntervalSeconds > 3600 || c.StaleAfterSeconds < 60 || c.StaleAfterSeconds > 86400 || c.NoProgressLimit < 1 || c.NoProgressLimit > 10 || len(c.ReadyStatuses) == 0 || len(c.ReadyStatuses) > 20 {
		return errors.New("invalid project supervision policy")
	}
	seen := map[string]bool{}
	for _, key := range c.ReadyStatuses {
		if seen[key] {
			return errors.New("duplicate ready status")
		}
		seen[key] = true
		if key == "" || key == "backlog" || key == "triage" || key == "done" || key == "cancelled" {
			return errors.New("paused and terminal statuses cannot be automatic ready statuses")
		}
	}
	return nil
}
func supervisionConfig(raw []byte) (ProjectSupervisionConfig, error) {
	c := DefaultProjectSupervisionConfig()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}

type ProjectCoordinationContext struct {
	Type           string `json:"type"`
	WorkspaceID    string `json:"workspace_id"`
	ProjectID      string `json:"project_id"`
	RequesterID    string `json:"requester_id"`
	PolicyRevision int64  `json:"policy_revision"`
	CheckedVersion int64  `json:"checked_version"`
	Fingerprint    string `json:"fingerprint"`
	Prompt         string `json:"prompt"`
}

func ProjectCoordination(task db.AgentTaskQueue) (ProjectCoordinationContext, bool) {
	var c ProjectCoordinationContext
	err := json.Unmarshal(task.Context, &c)
	return c, err == nil && c.Type == ProjectSupervisionContextType
}

type SupervisionIssue struct {
	ID           string  `json:"id"`
	Identifier   string  `json:"identifier"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Revision     int64   `json:"revision"`
	AssigneeType string  `json:"assignee_type"`
	AssigneeID   *string `json:"assignee_id"`
	Category     string  `json:"category"`
	Reason       string  `json:"reason"`
	ActiveRuns   int32   `json:"active_runs"`
	AgeSeconds   int64   `json:"age_seconds"`
}
type SupervisionCounts struct {
	Ready      int `json:"ready"`
	Unassigned int `json:"unassigned"`
	Executing  int `json:"executing"`
	Review     int `json:"review"`
	Blocked    int `json:"blocked"`
	Paused     int `json:"paused"`
	Stalled    int `json:"stalled"`
}
type ProjectSupervisionSnapshot struct {
	Counts             SupervisionCounts  `json:"counts"`
	Issues             []SupervisionIssue `json:"issues"`
	Fingerprint        string             `json:"fingerprint"`
	OldestReadySeconds int64              `json:"oldest_ready_seconds"`
	Actionable         int                `json:"actionable"`
}

func buildSupervisionSnapshot(rows []db.ListSupervisionIssuesRow, c ProjectSupervisionConfig, prefix string, now time.Time) ProjectSupervisionSnapshot {
	result := ProjectSupervisionSnapshot{Issues: []SupervisionIssue{}}
	ready := map[string]bool{}
	for _, status := range c.ReadyStatuses {
		ready[status] = true
	}
	fingerprints := []string{}
	for _, row := range rows {
		fingerprints = append(fingerprints, util.UUIDToString(row.ID)+":"+row.Status+":"+row.AssigneeType.String+":"+util.UUIDToString(row.AssigneeID)+":"+util.UUIDToString(row.ParentIssueID)+":"+strconv.Itoa(int(row.Stage.Int32))+":"+strconv.FormatBool(row.DependencyBlocked))
		if row.Status == "done" || row.Status == "cancelled" || row.StatusCategory == "done" || row.StatusCategory == "closed" {
			continue
		}
		age := int64(now.Sub(row.CreatedAt.Time).Seconds())
		if age < 0 {
			age = 0
		}
		item := SupervisionIssue{ID: util.UUIDToString(row.ID), Identifier: prefix + "-" + strconv.Itoa(int(row.Number)), Title: row.Title, Status: row.Status, Revision: row.Revision, AssigneeType: row.AssigneeType.String, AssigneeID: util.UUIDToPtr(row.AssigneeID), ActiveRuns: row.ActiveRuns, AgeSeconds: age}
		switch {
		case row.ActiveRuns > 0:
			item.Category = "executing"
			result.Counts.Executing++
		case row.Status == "in_review":
			item.Category = "review"
			result.Counts.Review++
			result.Actionable++
		case row.DependencyBlocked:
			item.Category = "blocked"
			item.Reason = "dependency"
			result.Counts.Blocked++
		case row.Status == "blocked":
			item.Category = "blocked"
			item.Reason = "explicit_block"
			result.Counts.Blocked++
			result.Actionable++
		case row.Status == "backlog" || row.Status == "triage":
			item.Category = "paused"
			item.Reason = "explicit_backlog"
			result.Counts.Paused++
		case !row.AssigneeID.Valid:
			item.Category = "unassigned"
			item.Reason = "missing_assignee"
			result.Counts.Unassigned++
			result.Actionable++
		case ready[row.Status]:
			item.Category = "ready"
			result.Counts.Ready++
			result.Actionable++
			if age > result.OldestReadySeconds {
				result.OldestReadySeconds = age
			}
		case row.StatusCategory == "started" && (row.LastRunStatus == "failed" || now.Sub(row.UpdatedAt.Time) >= time.Duration(c.StaleAfterSeconds)*time.Second):
			item.Category = "stalled"
			item.Reason = "no_active_run"
			result.Counts.Stalled++
			result.Actionable++
		default:
			item.Category = "paused"
			item.Reason = "status_not_ready"
			result.Counts.Paused++
		}
		result.Issues = append(result.Issues, item)
	}
	sort.Strings(fingerprints)
	payload, _ := json.Marshal(fingerprints)
	hash := sha256.Sum256(payload)
	result.Fingerprint = hex.EncodeToString(hash[:])
	return result
}

type ProjectSupervisionAction struct {
	IssueID      string `json:"issue_id"`
	Revision     int64  `json:"revision"`
	Kind         string `json:"kind"`
	AssigneeType string `json:"assignee_type,omitempty"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}
type ProjectSupervisionReport struct {
	TaskID         string `json:"task_id"`
	CheckedVersion int64  `json:"checked_version"`
	Decision       string `json:"decision"`
	Summary        string `json:"summary"`
	WaitReason     string `json:"wait_reason,omitempty"`
}
