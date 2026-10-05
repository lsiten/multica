package execenv

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// ProtectSharedLocalDirectory installs only a task-neutral CLI guard. Its
// release belongs to the last shared user, not any individual run's cleanup.
func ProtectSharedLocalDirectory(parent context.Context, path string) (func() error, error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	lease, err := UseSharedDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return lease.Finish(ctx, nil)
	}, nil
}

// InjectIsolatedRuntimeConfig exposes per-run instructions without editing
// the shared checkout's native instruction files.
func InjectIsolatedRuntimeConfig(contextDir, provider string, ctx TaskContextForEnv) (string, error) {
	brief, err := InjectRuntimeConfig(contextDir, provider, ctx)
	if err != nil {
		return "", err
	}
	skills := skillsDirPath(contextDir, provider)
	switch provider {
	case "codex":
		skills = filepath.Join(filepath.Dir(contextDir), codexHomeDirName, "skills")
	case "hermes":
		skills = filepath.Join(filepath.Dir(contextDir), "hermes-home", "skills")
	case "qwenpaw":
		skills = filepath.Join(filepath.Dir(contextDir), "qwenpaw-workspace", "skills")
	}
	brief += fmt.Sprintf("\n## Task context locations\n\nThis run's context files are under %q. Read project resources at %q. Load bound skills from %q by opening each skill's SKILL.md; these per-run skills may not be auto-discovered from the shared code directory. Keep these paths separate from the code working directory.\n", contextDir, filepath.Join(contextDir, ".multica", "project", "resources.json"), skills)
	return brief, nil
}
