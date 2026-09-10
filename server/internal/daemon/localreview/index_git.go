package localreview

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (tx *indexTransaction) git(ctx context.Context, input io.Reader, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options := []string{"--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false", "-C", tx.repository}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tx.scratchPath(), "GIT_LITERAL_PATHSPECS=1", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stdin = input
	output, stderr := &boundedOutput{}, &boundedOutput{}
	cmd.Stdout, cmd.Stderr = output, stderr
	if err := cmd.Run(); err != nil {
		return string(output.data), fmt.Errorf("git %s while preparing index: %w", args[0], err)
	}
	return strings.TrimSpace(string(output.data)), nil
}
