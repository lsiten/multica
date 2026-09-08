package execenv

// LockRepositoryForReview shares the task preparation lock and fails closed
// if cross-process exclusion cannot be established for a merge.
func LockRepositoryForReview(path string) (func(), error) {
	f, err := acquireGitRootFileLock(path, nil)
	if err != nil {
		return nil, err
	}
	unlock := lockGitRootInProcess(path)
	return func() { unlock(); unlockFile(f); f.Close() }, nil
}
