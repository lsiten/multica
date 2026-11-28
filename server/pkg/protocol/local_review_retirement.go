package protocol

const LocalReviewWritesRetired = "local review Git writes are no longer supported"

func IsRetiredLocalReviewAction(action string) bool {
	switch action {
	case "submit", "approve", "request_changes", "merge", "merge_selected", "stage", "unstage", "commit":
		return true
	default:
		return false
	}
}
