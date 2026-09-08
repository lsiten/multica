package localreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// InspectionFilterOptions disables command-producing conversions for this invocation
// only. Review compares raw working files; it never executes clean/process filters
// or modifies the repository's filter configuration.
func InspectionFilterOptions(ctx context.Context, path string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "-C", path, "config", "--null", "--name-only", "--get-regexp", `^filter\..*\.(clean|process)$`)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output := &boundedOutput{}
	command.Stdout = output
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect review filter configuration: %w", err)
	}
	seen := make(map[string]bool)
	var overrides []string
	for _, key := range strings.Split(string(output.data), "\x00") {
		if key == "" {
			continue
		}
		index := strings.LastIndexByte(key, '.')
		if index < 0 {
			return nil, errors.New("invalid Git filter key")
		}
		prefix := key[:index]
		if seen[prefix] {
			continue
		}
		if len(seen) >= 128 {
			return nil, errors.New("too many configured review filters")
		}
		seen[prefix] = true
		overrides = append(overrides, "-c", prefix+".clean=", "-c", prefix+".process=", "-c", prefix+".required=false")
	}
	return overrides, nil
}
