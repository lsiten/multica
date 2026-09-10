package localreview

import "strings"

type SelectedMergeConflictError struct{ Files []string }

func (e *SelectedMergeConflictError) Error() string { return ErrSelectedMergeConflict.Error() }
func (e *SelectedMergeConflictError) Unwrap() error { return ErrSelectedMergeConflict }

func selectedConflict(output string) error {
	parts := strings.Split(output, "\x00")
	if len(parts) == 0 || !validGitObjectID(parts[0]) {
		return ErrSelectedMergeConflict
	}
	files := []string{}
	for _, path := range parts[1:] {
		if path == "" {
			continue
		}
		if !validVersionPath(path) || len(files) >= maxVersionFiles {
			return ErrSelectedMergeConflict
		}
		files = append(files, path)
	}
	return &SelectedMergeConflictError{Files: files}
}
