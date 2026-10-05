package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type progressGraph struct {
	nodes        map[string]db.ListProgressIssuesRow
	children     map[string][]string
	dependencies map[string][]string
	dependents   map[string][]string
	requests     map[string][]ProgressRequest
	prefix       string
	coverage     map[string]bool
}

func newProgressGraph(rows []db.ListProgressIssuesRow, dependencies []db.IssueDependency, requests []db.HumanRequest, prefix, memberID string) progressGraph {
	g := progressGraph{nodes: map[string]db.ListProgressIssuesRow{}, children: map[string][]string{}, dependencies: map[string][]string{}, dependents: map[string][]string{}, requests: map[string][]ProgressRequest{}, prefix: prefix, coverage: map[string]bool{}}
	for _, row := range rows {
		id := util.UUIDToString(row.ID)
		g.nodes[id] = row
		if row.ParentIssueID.Valid {
			parent := util.UUIDToString(row.ParentIssueID)
			g.children[parent] = append(g.children[parent], id)
		}
	}
	for _, dependency := range dependencies {
		issue, blocker := util.UUIDToString(dependency.IssueID), util.UUIDToString(dependency.DependsOnIssueID)
		switch dependency.Type {
		case "blocks":
			issue, blocker = blocker, issue
		case "blocked_by":
		default:
			continue
		}
		_, hasIssue := g.nodes[issue]
		_, hasBlocker := g.nodes[blocker]
		if !hasIssue || !hasBlocker {
			g.coverage["missing_dependency"] = true
			continue
		}
		if !slices.Contains(g.dependencies[issue], blocker) {
			g.dependencies[issue] = append(g.dependencies[issue], blocker)
		}
		if !slices.Contains(g.dependents[blocker], issue) {
			g.dependents[blocker] = append(g.dependents[blocker], issue)
		}
	}
	for _, request := range requests {
		if !request.IssueID.Valid {
			continue
		}
		id := util.UUIDToString(request.IssueID)
		g.requests[id] = append(g.requests[id], progressRequestRef(request, memberID))
	}
	for _, relations := range []map[string][]string{g.children, g.dependencies, g.dependents} {
		for id := range relations {
			slices.Sort(relations[id])
		}
	}
	return g
}

func progressRequestRef(request db.HumanRequest, memberID string) ProgressRequest {
	return ProgressRequest{ID: util.UUIDToString(request.ID), RecipientID: util.UUIDToString(request.RecipientID), NeedsMe: util.UUIDToString(request.RecipientID) == memberID, ExpiresAt: request.ExpiresAt.Time.UTC().Format(time.RFC3339Nano)}
}

func progressCategory(row db.ListProgressIssuesRow) string {
	switch row.StatusCategory {
	case "done", "closed", "started", "unstarted":
		return row.StatusCategory
	}
	switch row.Status {
	case "done":
		return "done"
	case "cancelled":
		return "closed"
	case "backlog", "todo", "triage":
		return "unstarted"
	case "in_progress", "in_review", "blocked":
		return "started"
	default:
		return "unknown"
	}
}

func progressOpen(row db.ListProgressIssuesRow) bool {
	category := progressCategory(row)
	return category != "done" && category != "closed"
}

// Cancelled prerequisites remain unresolved under the existing supervision contract.
func progressBlocks(row db.ListProgressIssuesRow) bool {
	return row.Status == "cancelled" || progressOpen(row)
}

func (g *progressGraph) ref(id string) ProgressIssueRef {
	row := g.nodes[id]
	return ProgressIssueRef{ID: id, Identifier: fmt.Sprintf("%s-%d", g.prefix, row.Number), Title: row.Title, Status: row.Status, StatusCategory: progressCategory(row), StatusName: row.StatusName, ProjectID: util.UUIDToPtr(row.ProjectID), ProjectTitle: row.ProjectTitle}
}

func (g *progressGraph) path(id string) ([]ProgressIssueRef, bool) {
	path := []ProgressIssueRef{}
	seen := map[string]bool{id: true}
	row := g.nodes[id]
	for row.ParentIssueID.Valid {
		parent := util.UUIDToString(row.ParentIssueID)
		if seen[parent] {
			g.coverage["hierarchy_cycle"] = true
			return path, true
		}
		seen[parent] = true
		ancestor, ok := g.nodes[parent]
		if !ok {
			g.coverage["missing_parent"] = true
			break
		}
		path = append(path, g.ref(parent))
		row = ancestor
		if len(path) >= 256 {
			g.coverage["path_limit"] = true
			break
		}
	}
	slices.Reverse(path)
	return path, false
}

func (g *progressGraph) descendants(id string) (int, bool) {
	open := 0
	seen := map[string]bool{id: true}
	stack := append([]string{}, g.children[id]...)
	cancelled := false
	for len(stack) > 0 {
		child := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[child] {
			g.coverage["hierarchy_cycle"] = true
			continue
		}
		seen[child] = true
		row := g.nodes[child]
		if progressOpen(row) {
			open++
		}
		if row.Status == "cancelled" {
			cancelled = true
		}
		stack = append(stack, g.children[child]...)
	}
	return open, cancelled
}

func (g *progressGraph) blockers(id string) ([]ProgressIssueRef, []ProgressIssueRef, bool, bool) {
	direct := []ProgressIssueRef{}
	roots := map[string]bool{}
	visiting, visited := map[string]bool{}, map[string]bool{}
	cycle, cancelled := false, false
	var visit func(string)
	visit = func(current string) {
		if visiting[current] {
			cycle = true
			g.coverage["dependency_cycle"] = true
			return
		}
		if visited[current] {
			return
		}
		visited[current], visiting[current] = true, true
		row := g.nodes[current]
		if row.Status == "cancelled" {
			cancelled = true
			roots[current] = true
			visiting[current] = false
			return
		}
		count := 0
		for _, blocker := range g.dependencies[current] {
			if progressBlocks(g.nodes[blocker]) {
				count++
				visit(blocker)
			}
		}
		if count == 0 && current != id {
			roots[current] = true
		}
		visiting[current] = false
	}
	for _, blocker := range g.dependencies[id] {
		if progressBlocks(g.nodes[blocker]) {
			direct = append(direct, g.ref(blocker))
		}
	}
	visit(id)
	rootIDs := make([]string, 0, len(roots))
	for root := range roots {
		rootIDs = append(rootIDs, root)
	}
	slices.Sort(rootIDs)
	rootRefs := make([]ProgressIssueRef, 0, len(rootIDs))
	for _, root := range rootIDs {
		rootRefs = append(rootRefs, g.ref(root))
	}
	return direct, rootRefs, cycle, cancelled
}

func (g *progressGraph) impact(id string) int {
	if !progressBlocks(g.nodes[id]) {
		return 0
	}
	seen := map[string]bool{id: true}
	stack := append([]string{}, g.dependents[id]...)
	count := 0
	for len(stack) > 0 {
		dependent := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[dependent] {
			continue
		}
		seen[dependent] = true
		row := g.nodes[dependent]
		if !progressOpen(row) {
			continue
		}
		if row.InScope {
			count++
		}
		stack = append(stack, g.dependents[dependent]...)
	}
	return count
}

func (g *progressGraph) stageReason(id string) string {
	row := g.nodes[id]
	if !row.Stage.Valid || !row.ParentIssueID.Valid || row.Status != "backlog" || row.ActiveRunID.Valid {
		return ""
	}
	parent := util.UUIDToString(row.ParentIssueID)
	parentRow, exists := g.nodes[parent]
	if !exists || !progressOpen(parentRow) || parentRow.Status == "backlog" || parentRow.Status == "triage" {
		return ""
	}
	prior, cancelled := false, false
	for _, sibling := range g.children[parent] {
		previous := g.nodes[sibling]
		if !previous.Stage.Valid || previous.Stage.Int32 >= row.Stage.Int32 {
			continue
		}
		prior = true
		if progressOpen(previous) {
			return "stage_waiting"
		}
		if previous.Status == "cancelled" {
			cancelled = true
		}
	}
	if cancelled {
		return "cancelled_stage"
	}
	if prior {
		return "stage_ready"
	}
	return ""
}

func progressPriority(priority string) int {
	switch priority {
	case "urgent":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

func progressRank(reasons []string) int {
	rank := 6
	for _, reason := range reasons {
		candidate := 6
		switch reason {
		case "pending_me":
			candidate = 0
		case "hierarchy_cycle", "dependency_cycle", "parent_closed_with_open_children", "cancelled_dependency", "cancelled_children", "cancelled_stage", "status_unknown":
			candidate = 1
		case "dependency", "blocks_others", "explicit_block", "run_failed", "runtime_missing", "runtime_offline", "assignee_unavailable", "stalled", "pending_other":
			candidate = 2
		case "review", "parent_wrap_up":
			candidate = 3
		case "unassigned", "stage_ready":
			candidate = 4
		case "ready":
			candidate = 5
		}
		if candidate < rank {
			rank = candidate
		}
	}
	return rank
}

func (g *progressGraph) entry(id string, now time.Time, staleAfter time.Duration, scope ProgressScope) ProgressEntry {
	row := g.nodes[id]
	path, hierarchyCycle := g.path(id)
	waiting, cancelledChildren := g.descendants(id)
	direct, roots, dependencyCycle, cancelledDependency := g.blockers(id)
	entry := ProgressEntry{Issue: g.ref(id), Priority: row.Priority, AssigneeType: row.AssigneeType.String, AssigneeID: util.UUIDToPtr(row.AssigneeID), Path: path, Reasons: []string{}, WaitingChildren: waiting, DirectBlockers: direct, RootBlockers: roots, BlockedIssueCount: g.impact(id), Requests: append([]ProgressRequest{}, g.requests[id]...), sortPriority: progressPriority(row.Priority), sortWait: row.StatusChangedAt.Time}
	if row.Stage.Valid {
		value := row.Stage.Int32
		entry.Stage = &value
	}
	if row.DueDate.Valid {
		date := row.DueDate.Time.Format("2006-01-02")
		entry.DueDate = &date
		entry.sortOverdue = date < now.Format("2006-01-02")
	}
	for _, ancestor := range path {
		ancestorRow := g.nodes[ancestor.ID]
		if ancestorRow.InScope && progressOpen(ancestorRow) {
			entry.AffectedGoalCount++
		}
		if priority := progressPriority(ancestorRow.Priority); priority < entry.sortPriority {
			entry.sortPriority = priority
		}
		if entry.Group == nil && ancestor.ID != scope.ID && ancestorRow.InScope {
			reference := ancestor
			entry.Group = &reference
		}
	}
	if entry.Group == nil {
		reference := entry.Issue
		entry.Group = &reference
	}
	if hierarchyCycle {
		entry.Reasons = append(entry.Reasons, "hierarchy_cycle")
	}
	if dependencyCycle {
		entry.Reasons = append(entry.Reasons, "dependency_cycle")
	}
	if !progressOpen(row) && waiting > 0 {
		entry.Reasons = append(entry.Reasons, "parent_closed_with_open_children")
	}
	if entry.BlockedIssueCount > 0 && !row.ActiveRunID.Valid {
		entry.Reasons = append(entry.Reasons, "blocks_others")
	}
	for _, request := range entry.Requests {
		if request.NeedsMe {
			entry.NeedsMe = true
			if !slices.Contains(entry.Reasons, "pending_me") {
				entry.Reasons = append(entry.Reasons, "pending_me")
			}
		} else if !slices.Contains(entry.Reasons, "pending_other") {
			entry.Reasons = append(entry.Reasons, "pending_other")
		}
	}
	if progressOpen(row) {
		if len(direct) > 0 {
			entry.Reasons = append(entry.Reasons, "dependency")
		}
		if cancelledDependency {
			entry.Reasons = append(entry.Reasons, "cancelled_dependency")
		}
		if progressCategory(row) == "unknown" {
			entry.Reasons = append(entry.Reasons, "status_unknown")
			g.coverage["unknown_status"] = true
		}
		if row.Status == "blocked" {
			entry.Reasons = append(entry.Reasons, "explicit_block")
		}
		if row.Status == "in_review" {
			entry.Reasons = append(entry.Reasons, "review")
		}
		stage := g.stageReason(id)
		if stage != "" {
			entry.Reasons = append(entry.Reasons, stage)
		}
		if waiting > 0 {
			entry.Reasons = append(entry.Reasons, "waiting_children")
		} else if len(g.children[id]) > 0 && row.Status != "in_review" {
			entry.Reasons = append(entry.Reasons, "parent_wrap_up")
			if cancelledChildren {
				entry.Reasons = append(entry.Reasons, "cancelled_children")
			}
		}
		if !row.ActiveRunID.Valid {
			if row.LatestRunStatus == "failed" {
				entry.Reasons = append(entry.Reasons, "run_failed")
			}
			if row.LatestRunAt.Valid && row.LatestRunAt.Time.After(entry.sortWait) {
				entry.sortWait = row.LatestRunAt.Time
			}
			parked := row.Status == "backlog" || row.Status == "triage"
			waitingNormally := len(g.children[id]) > 0 || len(direct) > 0 || len(entry.Requests) > 0 || stage == "stage_waiting" || parked
			if !parked && (row.AssigneeType.String == "agent" || row.AssigneeType.String == "squad") {
				if row.AssigneeArchived || row.AssigneeMissing {
					entry.Reasons = append(entry.Reasons, "assignee_unavailable")
				} else if row.RuntimeMissing {
					entry.Reasons = append(entry.Reasons, "runtime_missing")
				} else if row.RuntimeStatus == "offline" {
					entry.Reasons = append(entry.Reasons, "runtime_offline")
				}
			}
			if !waitingNormally && row.Status != "in_review" {
				if !row.AssigneeID.Valid {
					entry.Reasons = append(entry.Reasons, "unassigned")
				}
				if row.AssigneeType.String == "agent" || row.AssigneeType.String == "squad" {
					if progressCategory(row) == "started" && now.Sub(entry.sortWait) >= staleAfter && row.LatestRunStatus != "failed" {
						entry.Reasons = append(entry.Reasons, "stalled")
					}
				}
				if progressCategory(row) == "unstarted" && row.AssigneeID.Valid && !row.AssigneeArchived && !row.AssigneeMissing && !row.RuntimeMissing && row.RuntimeStatus != "offline" {
					entry.Reasons = append(entry.Reasons, "ready")
				}
			}
		}
	}
	if row.ActiveRunID.Valid {
		entry.Run = &ProgressRun{ID: util.UUIDToString(row.ActiveRunID), Status: row.ActiveRunStatus, Since: row.ActiveRunAt.Time.UTC().Format(time.RFC3339Nano)}
	} else if row.LatestRunID.Valid {
		entry.Run = &ProgressRun{ID: util.UUIDToString(row.LatestRunID), Status: row.LatestRunStatus, Since: row.LatestRunAt.Time.UTC().Format(time.RFC3339Nano)}
	}
	entry.WaitSince = entry.sortWait.UTC().Format(time.RFC3339Nano)
	entry.Rank = progressRank(entry.Reasons)
	entry.Attention = entry.Rank < 6
	return entry
}

func buildIssueProgress(ctx context.Context, graph progressGraph, options ProgressOptions, now time.Time, staleAfter time.Duration) (IssueProgressView, error) {
	view := IssueProgressView{Scope: options.Scope, AsOf: now.UTC().Format(time.RFC3339Nano), Items: []ProgressEntry{}, ProjectRequests: []ProgressRequest{}, Complete: true, CoverageReasons: []string{}}
	ids := make([]string, 0, len(graph.nodes))
	for id := range graph.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return view, err
		}
		row := graph.nodes[id]
		if !row.InScope {
			continue
		}
		entry := graph.entry(id, now, staleAfter, options.Scope)
		if options.Today != "" && entry.DueDate != nil {
			entry.sortOverdue = *entry.DueDate < options.Today
		}
		if options.Scope.Type == "issue" && id == options.Scope.ID {
			view.Root = &entry
			continue
		}
		view.Summary.Total++
		switch progressCategory(row) {
		case "done":
			view.Summary.Done++
		case "closed":
			view.Summary.Closed++
		default:
			view.Summary.Open++
			if len(graph.children[id]) > 0 {
				view.Summary.OpenParent++
			} else {
				view.Summary.OpenLeaf++
			}
		}
		if entry.Attention {
			view.Summary.Attention++
		}
		view.Summary.PendingDecisions += len(entry.Requests)
		if progressOpen(row) || entry.Attention {
			view.Items = append(view.Items, entry)
		}
	}
	sort.SliceStable(view.Items, func(i, j int) bool {
		left, right := view.Items[i], view.Items[j]
		if left.Rank != right.Rank {
			return left.Rank < right.Rank
		}
		if left.sortPriority != right.sortPriority {
			return left.sortPriority < right.sortPriority
		}
		if left.sortOverdue != right.sortOverdue {
			return left.sortOverdue
		}
		if left.BlockedIssueCount != right.BlockedIssueCount {
			return left.BlockedIssueCount > right.BlockedIssueCount
		}
		if !left.sortWait.Equal(right.sortWait) {
			return left.sortWait.Before(right.sortWait)
		}
		return left.Issue.ID < right.Issue.ID
	})
	for reason := range graph.coverage {
		view.CoverageReasons = append(view.CoverageReasons, reason)
	}
	slices.Sort(view.CoverageReasons)
	view.Complete = len(view.CoverageReasons) == 0
	return view, nil
}
