package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	// ErrProgressSnapshotChanged requires a fresh first page after recorded facts change.
	ErrProgressSnapshotChanged = errors.New("progress snapshot changed")
	// ErrProgressCursorInvalid rejects a cursor that does not belong to this view.
	ErrProgressCursorInvalid = errors.New("invalid progress cursor")
)

// IssueProgressService reads work inside a consistent snapshot without advancing it.
type IssueProgressService struct{ TxStarter TxStarter }

// View computes either an issue subtree or a project's attention projection.
func (s *IssueProgressService) View(ctx context.Context, workspaceID pgtype.UUID, options ProgressOptions) (IssueProgressView, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return IssueProgressView{}, fmt.Errorf("begin progress snapshot: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return IssueProgressView{}, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout = '5000ms'"); err != nil {
		return IssueProgressView{}, err
	}
	queries := db.New(tx)
	view, err := readIssueProgress(ctx, queries, workspaceID, options)
	if err != nil {
		return view, err
	}
	if err = tx.Commit(ctx); err != nil {
		return view, err
	}
	return view, nil
}

func readIssueProgress(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, options ProgressOptions) (IssueProgressView, error) {
	id, err := util.ParseUUID(options.Scope.ID)
	if err != nil {
		return IssueProgressView{}, err
	}
	memberID, err := util.ParseUUID(options.MemberID)
	if err != nil {
		return IssueProgressView{}, err
	}
	params := db.ListProgressIssuesParams{WorkspaceID: workspaceID}
	configProject := pgtype.UUID{}
	switch options.Scope.Type {
	case "issue":
		params.RootID = id
		root, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
		if err != nil {
			return IssueProgressView{}, err
		}
		configProject = root.ProjectID
	case "project":
		params.ProjectID, configProject = id, id
		if _, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID}); err != nil {
			return IssueProgressView{}, err
		}
	default:
		return IssueProgressView{}, errors.New("invalid progress scope")
	}
	rows, err := q.ListProgressIssues(ctx, params)
	if err != nil {
		return IssueProgressView{}, fmt.Errorf("load progress issues: %w", err)
	}
	coverage := []string{}
	if len(rows) > 10000 {
		rows = rows[:10000]
		coverage = append(coverage, "issue_limit")
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	dependencies, err := q.ListProgressDependencies(ctx, db.ListProgressDependenciesParams{WorkspaceID: workspaceID, IssueIds: ids})
	if err != nil {
		return IssueProgressView{}, fmt.Errorf("load progress dependencies: %w", err)
	}
	if len(dependencies) > 20000 {
		dependencies = dependencies[:20000]
		coverage = append(coverage, "dependency_limit")
	}
	requests, err := q.ListProgressHumanRequests(ctx, db.ListProgressHumanRequestsParams{WorkspaceID: workspaceID, IssueIds: ids, ProjectID: params.ProjectID, MemberID: memberID})
	if err != nil {
		return IssueProgressView{}, fmt.Errorf("load progress requests: %w", err)
	}
	if len(requests) > 1000 {
		requests = requests[:1000]
		coverage = append(coverage, "request_limit")
	}
	validRequests := make([]db.HumanRequest, 0, len(requests))
	validator := &TaskService{Queries: q}
	checkedSources := map[string]error{}
	for _, request := range requests {
		key := util.UUIDToString(request.SourceTaskID) + ":" + request.ScopeFingerprint + ":" + util.UUIDToString(request.RecipientID)
		validationErr, checked := checkedSources[key]
		if !checked {
			source, loadErr := q.GetAgentTask(ctx, request.SourceTaskID)
			if errors.Is(loadErr, pgx.ErrNoRows) {
				coverage = append(coverage, "missing_request_source")
				checkedSources[key] = loadErr
				continue
			}
			if loadErr != nil {
				return IssueProgressView{}, loadErr
			}
			validationErr = validator.ValidateHumanResponseScope(ctx, request, source)
			checkedSources[key] = validationErr
		}
		if errors.Is(validationErr, ErrHumanRequestConflict) || errors.Is(validationErr, ErrHumanRequestForbidden) || errors.Is(validationErr, pgx.ErrNoRows) {
			continue
		}
		if validationErr != nil {
			return IssueProgressView{}, validationErr
		}
		validRequests = append(validRequests, request)
	}
	workspace, err := q.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return IssueProgressView{}, err
	}
	staleAfter := time.Duration(DefaultProjectSupervisionConfig().StaleAfterSeconds) * time.Second
	if configProject.Valid {
		policy, policyErr := q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{WorkspaceID: workspaceID, ProjectID: configProject})
		if policyErr != nil && !errors.Is(policyErr, pgx.ErrNoRows) {
			return IssueProgressView{}, policyErr
		}
		if policyErr == nil {
			config, configErr := supervisionConfig(policy.Config)
			if configErr != nil {
				return IssueProgressView{}, configErr
			}
			staleAfter = time.Duration(config.StaleAfterSeconds) * time.Second
		}
	}
	graph := newProgressGraph(rows, dependencies, validRequests, workspace.IssuePrefix, options.MemberID)
	for _, reason := range coverage {
		graph.coverage[reason] = true
	}
	view, err := buildIssueProgress(ctx, graph, options, time.Now(), staleAfter)
	if err != nil {
		return view, err
	}
	view.WorkspaceID = util.UUIDToString(workspaceID)
	for _, request := range validRequests {
		if !request.IssueID.Valid && options.Scope.Type == "project" {
			view.ProjectRequests = append(view.ProjectRequests, progressRequestRef(request, options.MemberID))
			view.Summary.PendingDecisions++
		}
	}
	return paginateIssueProgress(view, options)
}

type progressCursor struct {
	Version string `json:"version"`
	Query   string `json:"query"`
	Offset  int    `json:"offset"`
}

func paginateIssueProgress(view IssueProgressView, options ProgressOptions) (IssueProgressView, error) {
	facts, err := json.Marshal(struct {
		Summary  ProgressSummary
		Root     *ProgressEntry
		Items    []ProgressEntry
		Requests []ProgressRequest
		Coverage []string
	}{view.Summary, view.Root, view.Items, view.ProjectRequests, view.CoverageReasons})
	if err != nil {
		return view, err
	}
	hash := sha256.Sum256(facts)
	view.Version = hex.EncodeToString(hash[:])
	queryBytes, err := json.Marshal(struct {
		Scope                                             ProgressScope
		Filter, AssigneeType, AssigneeID, MemberID, Today string
		OnlyMine                                          bool
	}{options.Scope, options.Filter, options.AssigneeType, options.AssigneeID, options.MemberID, options.Today, options.OnlyMine})
	if err != nil {
		return view, err
	}
	queryHash := sha256.Sum256(queryBytes)
	query := hex.EncodeToString(queryHash[:])
	filtered := []ProgressEntry{}
	for _, entry := range view.Items {
		if options.Scope.Type == "project" && !entry.Attention {
			continue
		}
		if options.OnlyMine && !entry.NeedsMe {
			continue
		}
		if options.AssigneeType != "" && entry.AssigneeType != options.AssigneeType {
			continue
		}
		if options.AssigneeID != "" && (entry.AssigneeID == nil || *entry.AssigneeID != options.AssigneeID) {
			continue
		}
		if !progressMatchesFilter(entry, options.Filter) {
			continue
		}
		filtered = append(filtered, entry)
	}
	view.FilteredTotal = len(filtered)
	limit := options.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return view, errors.New("progress limit must be between 1 and 200")
	}
	offset := 0
	if options.Cursor != "" {
		if len(options.Cursor) > 1024 {
			return view, ErrProgressCursorInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(options.Cursor)
		if err != nil {
			return view, ErrProgressCursorInvalid
		}
		var cursor progressCursor
		if json.Unmarshal(raw, &cursor) != nil || cursor.Query != query || cursor.Offset < 0 {
			return view, ErrProgressCursorInvalid
		}
		if cursor.Version != view.Version {
			return view, ErrProgressSnapshotChanged
		}
		offset = cursor.Offset
		if offset > len(filtered) {
			return view, ErrProgressCursorInvalid
		}
	}
	end := min(offset+limit, len(filtered))
	view.Items = filtered[offset:end]
	view.HasMore = end < len(filtered)
	if view.HasMore {
		raw, err := json.Marshal(progressCursor{Version: view.Version, Query: query, Offset: end})
		if err != nil {
			return view, err
		}
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		view.NextCursor = &cursor
	}
	return view, nil
}

func progressMatchesFilter(entry ProgressEntry, filter string) bool {
	switch filter {
	case "", "all":
		return true
	case "blocked":
		return slices.ContainsFunc(entry.Reasons, func(reason string) bool {
			return slices.Contains([]string{"dependency", "blocks_others", "explicit_block", "dependency_cycle", "cancelled_dependency", "runtime_missing", "runtime_offline", "assignee_unavailable"}, reason)
		})
	case "review":
		return slices.Contains(entry.Reasons, "review") || slices.Contains(entry.Reasons, "parent_wrap_up")
	case "follow_up":
		return entry.Attention
	case "ready":
		return slices.Contains(entry.Reasons, "ready") || slices.Contains(entry.Reasons, "unassigned") || slices.Contains(entry.Reasons, "stage_ready")
	default:
		return false
	}
}
