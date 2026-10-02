package handler

import (
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"sort"
	"time"
)

type projectCollaborationCounts struct {
	TaskCount      int `json:"task_count"`
	ActiveCount    int `json:"active_count"`
	EndedCount     int `json:"ended_count"`
	RunCount       int `json:"run_count"`
	RunningCount   int `json:"running_count"`
	FailedCount    int `json:"failed_count"`
	QueuedCount    int `json:"queued_count"`
	CompletedCount int `json:"completed_count"`
}
type projectCollaborationNode struct {
	ID     string                     `json:"id"`
	Type   string                     `json:"type"`
	Label  string                     `json:"label"`
	Status string                     `json:"status"`
	Data   projectCollaborationCounts `json:"data"`
}
type projectCollaborationEdge struct {
	SourceIncomplete bool   `json:"source_incomplete"`
	ID               string `json:"id"`
	From             string `json:"from"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Count            int    `json:"count"`
	ActiveCount      int    `json:"active_count"`
	EndedCount       int    `json:"ended_count"`
	EvidenceCount    int    `json:"evidence_count"`
	LastEventAt      string `json:"last_event_at"`
}
type projectCollaborationSummary struct {
	TaskCount   int    `json:"task_count"`
	AgentCount  int    `json:"agent_count"`
	ActiveCount int    `json:"active_count"`
	RunCount    int    `json:"run_count"`
	Coverage    string `json:"coverage"`
	Scope       string `json:"scope"`
}
type projectCollaborationGraph struct {
	projectCollaborationDataset
	Nodes   []projectCollaborationNode  `json:"nodes"`
	Edges   []projectCollaborationEdge  `json:"edges"`
	Summary projectCollaborationSummary `json:"summary"`
}

func collaborationActive(status string) bool {
	return status == "running" || status == "dispatched" || status == "waiting_local_directory"
}
func collaborationTaskActive(row db.ListProjectCollaborationRunsRow) bool { return row.TaskActive }
func collaborationTaskKey(row db.ListProjectCollaborationRunsRow) string {
	if row.IssueID.Valid {
		return "issue:" + uuidToString(row.IssueID)
	}
	return ""
}
func collaborationEdgeID(row db.ListProjectCollaborationRunsRow) string {
	if !row.SourceAgentID.Valid || row.SourceAgentID == row.AgentID {
		return ""
	}
	return "agent:" + uuidToString(row.SourceAgentID) + "->agent:" + uuidToString(row.AgentID) + ":" + row.RelationType
}
func buildProjectCollaboration(data projectCollaborationDataset) projectCollaborationGraph {
	out := projectCollaborationGraph{projectCollaborationDataset: data, Nodes: []projectCollaborationNode{}, Edges: []projectCollaborationEdge{}, Summary: projectCollaborationSummary{Coverage: "complete", Scope: "filtered_tasks", RunCount: len(data.Runs)}}
	nodes := map[string]*projectCollaborationNode{}
	edges := map[string]*projectCollaborationEdge{}
	allTasks := map[string]bool{}
	nodeTasks := map[string]map[string]bool{}
	edgeTasks := map[string]map[string]bool{}
	latestRuns := map[string]map[string]db.ListProjectCollaborationRunsRow{}
	for _, row := range data.Runs {
		taskKey := collaborationTaskKey(row)
		if taskKey != "" {
			allTasks[taskKey] = allTasks[taskKey] || row.TaskActive
		}
		id := "agent:" + uuidToString(row.AgentID)
		if nodes[id] == nil {
			nodes[id] = &projectCollaborationNode{ID: id, Type: "agent", Label: row.AgentName, Status: "idle"}
			nodeTasks[id] = map[string]bool{}
		}
		node := nodes[id]
		if latestRuns[id] == nil {
			latestRuns[id] = map[string]db.ListProjectCollaborationRunsRow{}
		}
		stateKey := taskKey
		if stateKey == "" {
			stateKey = uuidToString(row.ID)
		}
		previous, exists := latestRuns[id][stateKey]
		if !exists || row.CreatedAt.Time.After(previous.CreatedAt.Time) || (row.CreatedAt.Time.Equal(previous.CreatedAt.Time) && uuidToString(row.ID) > uuidToString(previous.ID)) {
			latestRuns[id][stateKey] = row
		}
		if taskKey != "" {
			nodeTasks[id][taskKey] = nodeTasks[id][taskKey] || row.TaskActive
		}
		node.Data.RunCount++
		switch {
		case collaborationActive(row.Status):
			node.Data.RunningCount++
		case row.Status == "queued":
			node.Data.QueuedCount++
		case row.Status == "failed":
			node.Data.FailedCount++
		case row.Status == "completed":
			node.Data.CompletedCount++
		}
		key := collaborationEdgeID(row)
		if key == "" {
			continue
		}
		parentID := "agent:" + uuidToString(row.SourceAgentID)
		if nodes[parentID] == nil {
			nodes[parentID] = &projectCollaborationNode{ID: parentID, Type: "agent", Label: row.SourceAgentName, Status: "idle"}
			nodeTasks[parentID] = map[string]bool{}
		}
		if taskKey != "" {
			nodeTasks[parentID][taskKey] = nodeTasks[parentID][taskKey] || row.TaskActive
		}
		if edges[key] == nil {
			edges[key] = &projectCollaborationEdge{ID: key, From: parentID, To: id, Type: row.RelationType}
			edgeTasks[key] = map[string]bool{}
		}
		edge := edges[key]
		edge.SourceIncomplete = edge.SourceIncomplete || (!row.SourceTaskID.Valid && !row.SourceIssueID.Valid)
		edge.EvidenceCount++
		if taskKey != "" {
			edgeTasks[key][taskKey] = edgeTasks[key][taskKey] || row.TaskActive
		}
		at := row.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		if edge.LastEventAt == "" || at > edge.LastEventAt {
			edge.LastEventAt = at
		}
	}
	for _, active := range allTasks {
		out.Summary.TaskCount++
		if active {
			out.Summary.ActiveCount++
		}
	}
	for id, node := range nodes {
		node.Data.TaskCount = len(nodeTasks[id])
		for _, active := range nodeTasks[id] {
			if active {
				node.Data.ActiveCount++
			} else {
				node.Data.EndedCount++
			}
		}
		switch {
		case node.Data.RunningCount > 0:
			node.Status = "running"
		case node.Data.QueuedCount > 0:
			node.Status = "queued"
		default:
			for _, latest := range latestRuns[id] {
				if latest.Status == "failed" {
					node.Status = "failed"
					break
				}
			}
		}
		out.Nodes = append(out.Nodes, *node)
	}
	for id, edge := range edges {
		edge.Count = len(edgeTasks[id])
		for _, active := range edgeTasks[id] {
			if active {
				edge.ActiveCount++
			} else {
				edge.EndedCount++
			}
		}
		out.Edges = append(out.Edges, *edge)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Data.ActiveCount != out.Nodes[j].Data.ActiveCount {
			return out.Nodes[i].Data.ActiveCount > out.Nodes[j].Data.ActiveCount
		}
		return out.Nodes[i].ID < out.Nodes[j].ID
	})
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].ActiveCount != out.Edges[j].ActiveCount {
			return out.Edges[i].ActiveCount > out.Edges[j].ActiveCount
		}
		return out.Edges[i].ID < out.Edges[j].ID
	})
	out.Summary.AgentCount = len(out.Nodes)
	if len(data.Runs) == 0 {
		out.Summary.Coverage = "empty"
	} else if len(data.CoverageReasons) > 0 {
		out.Summary.Coverage = "partial"
	}
	return out
}
