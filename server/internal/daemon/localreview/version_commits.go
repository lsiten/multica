package localreview

import (
	"context"
	"strconv"
	"strings"
)

type VersionCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}
type VersionCommitPage struct {
	Commits    []VersionCommit `json:"commits"`
	NextOffset int             `json:"next_offset"`
	HasMore    bool            `json:"has_more"`
}

func (v ReviewVersion) CommitPage(ctx context.Context, request FilePageRequest) (VersionCommitPage, error) {
	page := VersionCommitPage{Commits: []VersionCommit{}, NextOffset: request.Offset}
	if request.Offset < 0 || request.Limit < 1 || request.Limit > 100 {
		return page, ErrInvalidReviewVersion
	}
	raw, err := git(ctx, v.Header.Repository, "log", "--format=%H%x09%s", "--max-count="+strconv.Itoa(request.Limit+1), "--skip="+strconv.Itoa(request.Offset), v.Header.TargetHead+".."+v.Header.Head)
	if err != nil {
		return page, err
	}
	raw = strings.TrimSuffix(raw, "\n")
	if raw == "" {
		return page, nil
	}
	lines := strings.Split(raw, "\n")
	page.HasMore = len(lines) > request.Limit
	for _, line := range lines[:min(len(lines), request.Limit)] {
		sha, subject, ok := strings.Cut(line, "\t")
		if !ok || !validGitObjectID(sha) {
			return page, ErrInvalidReviewVersion
		}
		page.Commits = append(page.Commits, VersionCommit{SHA: sha, Subject: subject})
	}
	page.NextOffset += len(page.Commits)
	return page, nil
}
