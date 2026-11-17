package inbox

func NotificationMuted(preferences map[string]string, kind string) bool {
	group := ""
	switch kind {
	case "issue_assigned", "unassigned", "assignee_changed":
		group = "assignments"
	case "status_changed":
		group = "status_changes"
	case "new_comment":
		group = "comments"
	case "mentioned":
		group = "mentions"
	case "issue_created":
		group = "new_issues"
	case "priority_changed", "start_date_changed", "due_date_changed":
		group = "updates"
	case "task_completed", "task_failed", "agent_blocked", "agent_completed", "human_action_requested":
		group = "agent_activity"
	default:
		return false
	}
	return preferences[group] == "muted"
}
