package handler

import (
	"encoding/json"
	"sort"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type projectCollaborationCounts struct {
	TaskCount   int `json:"task_count"`
	ActiveCount int `json:"active_count"`
	FailedCount int `json:"failed_count"`
	QueuedCount int `json:"queued_count"`
}
type projectCollaborationNode struct {
	ID     string                     `json:"id"`
	Type   string                     `json:"type"`
	Label  string                     `json:"label"`
	Status string                     `json:"status"`
	Data   projectCollaborationCounts `json:"data"`
}
type projectCollaborationEdge struct {
	ID            string `json:"id"`
	From          string `json:"from"`
	To            string `json:"to"`
	Type          string `json:"type"`
	Count         int    `json:"count"`
	ActiveCount   int    `json:"active_count"`
	EvidenceCount int    `json:"evidence_count"`
	LastEventAt   string `json:"last_event_at"`
}
type projectCollaborationSummary struct {
	TaskCount   int    `json:"task_count"`
	AgentCount  int    `json:"agent_count"`
	ActiveCount int    `json:"active_count"`
	Coverage    string `json:"coverage"`
	Scope       string `json:"scope"`
}
type projectCollaborationGraph struct {
	projectCollaborationDataset
	Nodes   []projectCollaborationNode  `json:"nodes"`
	Edges   []projectCollaborationEdge  `json:"edges"`
	Summary projectCollaborationSummary `json:"summary"`
}

func graphAttachmentIDs(value any) []string {
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		raw = encoded
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return nil
	}
	return ids
}

func collaborationActive(status string) bool {
	return status == "running" || status == "dispatched" || status == "waiting_local_directory"
}
func collaborationEdgeID(row db.ListProjectCollaborationRunsRow) string {
	if !row.SourceAgentID.Valid || row.SourceAgentID == row.AgentID {
		return ""
	}
	return "agent:" + uuidToString(row.SourceAgentID) + "->agent:" + uuidToString(row.AgentID) + ":" + row.RelationType
}
func buildProjectCollaboration(data projectCollaborationDataset) projectCollaborationGraph {
	out := projectCollaborationGraph{projectCollaborationDataset: data, Nodes: []projectCollaborationNode{}, Edges: []projectCollaborationEdge{}, Summary: projectCollaborationSummary{TaskCount: len(data.Runs), Coverage: "complete", Scope: "filtered_runs"}}
	nodes := map[string]*projectCollaborationNode{}
	edges := map[string]*projectCollaborationEdge{}
	visibleTasks := make(map[string]struct{}, len(data.Runs)+len(data.GraphEvents))
	for _, row := range data.Runs {
		visibleTasks[uuidToString(row.ID)] = struct{}{}
	}
	for _, event := range data.GraphEvents {
		visibleTasks[uuidToString(event.TaskID)] = struct{}{}
	}
	for _, row := range data.Runs {
		id := "agent:" + uuidToString(row.AgentID)
		node := nodes[id]
		if node == nil {
			node = &projectCollaborationNode{ID: id, Type: "agent", Label: row.AgentName, Status: "idle"}
			nodes[id] = node
		}
		node.Data.TaskCount++
		switch {
		case collaborationActive(row.Status):
			node.Data.ActiveCount++
			out.Summary.ActiveCount++
		case row.Status == "queued":
			node.Data.QueuedCount++
		case row.Status == "failed":
			node.Data.FailedCount++
		}
		key := collaborationEdgeID(row)
		if key == "" {
			continue
		}
		parentID := "agent:" + uuidToString(row.SourceAgentID)
		if nodes[parentID] == nil {
			nodes[parentID] = &projectCollaborationNode{ID: parentID, Type: "agent", Label: row.SourceAgentName, Status: "idle"}
		}
		edge := edges[key]
		if edge == nil {
			edge = &projectCollaborationEdge{ID: key, From: parentID, To: id, Type: row.RelationType}
			edges[key] = edge
		}
		edge.Count++
		edge.EvidenceCount++
		if collaborationActive(row.Status) {
			edge.ActiveCount++
		}
		at := row.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		if edge.LastEventAt == "" {
			edge.LastEventAt = at
		}
	}
	// Artifact nodes and provenance edges are built exclusively from persisted
	// graph events visible to this member. We never infer an artifact from a
	// task without an event or attachment evidence.
	for _, event := range data.GraphEvents {
		var payload map[string]any
		if len(event.Data) > 0 && json.Unmarshal(event.Data, &payload) != nil {
			payload = nil
		}
		artifact, hasArtifact := payload["artifact"].(map[string]any)
		attachmentIDs := graphAttachmentIDs(event.ArtifactAttachmentIds)
		hasAttachments := len(attachmentIDs) > 0
		if !hasArtifact && !hasAttachments {
			continue
		}
		taskUUID := uuidToString(event.TaskID)
		if taskUUID == "" {
			continue
		}
		taskID := "task:" + taskUUID
		if _, exists := nodes[taskID]; !exists {
			nodes[taskID] = &projectCollaborationNode{ID: taskID, Type: "task", Label: "任务 " + uuidToString(event.TaskID)[:8], Status: "completed"}
		}
		artifactID := "artifact:" + uuidToString(event.ID)
		artifactNode := &projectCollaborationNode{ID: artifactID, Type: "artifact", Label: "成果 " + uuidToString(event.ID)[:8], Status: "completed"}
		if hasAttachments {
			artifactNode.Data.TaskCount = 1
		}
		nodes[artifactID] = artifactNode
		producedID := taskID + "->" + artifactID + ":produced"
		edges[producedID] = &projectCollaborationEdge{ID: producedID, From: taskID, To: artifactID, Type: "produced", Count: 1, EvidenceCount: 1, LastEventAt: event.CreatedAt.Time.UTC().Format(time.RFC3339Nano)}
		if source, ok := artifact["derived_from_task_id"].(string); ok && source != "" {
			// The source task must itself be visible to this member. This prevents
			// nested provenance fields from becoming an identifier side channel.
			if _, visible := visibleTasks[source]; !visible {
				continue
			}
			sourceID := "task:" + source
			if _, exists := nodes[sourceID]; !exists {
				nodes[sourceID] = &projectCollaborationNode{ID: sourceID, Type: "task", Label: "任务 " + source[:min(8, len(source))], Status: "completed"}
			}
			derivedID := sourceID + "->" + artifactID + ":derived_from"
			edges[derivedID] = &projectCollaborationEdge{ID: derivedID, From: sourceID, To: artifactID, Type: "derived_from", Count: 1, EvidenceCount: 1, LastEventAt: event.CreatedAt.Time.UTC().Format(time.RFC3339Nano)}
		}
	}
	for _, node := range nodes {
		switch {
		case node.Data.ActiveCount > 0:
			node.Status = "running"
		case node.Data.QueuedCount > 0:
			node.Status = "queued"
		case node.Data.FailedCount > 0:
			node.Status = "failed"
		}
		out.Nodes = append(out.Nodes, *node)
	}
	for _, edge := range edges {
		out.Edges = append(out.Edges, *edge)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].ID < out.Edges[j].ID })
	for _, node := range nodes {
		if node.Type == "agent" {
			out.Summary.AgentCount++
		}
	}
	if len(data.Runs) == 0 {
		out.Summary.Coverage = "empty"
	} else if len(data.CoverageReasons) > 0 {
		out.Summary.Coverage = "partial"
	}
	return out
}
