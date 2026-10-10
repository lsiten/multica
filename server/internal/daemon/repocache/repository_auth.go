package repocache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
)

// RepositoryAuth authorizes one repository and supplies only its Git helper
// credentials. The cache never persists this reply or changes process globals.
type RepositoryAuth interface {
	GitEnvironment(context.Context, string, string) (map[string]string, error)
}

type repositoryAuthContextKey struct{}

// NewWithRepositoryAuth installs a physical-owner credential callback. New keeps
// the legacy environment unchanged; only this explicit consumer uses isolation.
func NewWithRepositoryAuth(root string, logger *slog.Logger, auth RepositoryAuth) *Cache {
	cache := New(root, logger)
	cache.repositoryAuth = auth
	return cache
}

func validRepositoryAuthKey(key string) bool {
	switch key {
	case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
		return true
	default:
		return false
	}
}

func (c *Cache) repositoryContext(ctx context.Context, workspaceID, repoURL string) (context.Context, func(), error) {
	if c.repositoryAuth == nil {
		return ctx, func() {}, nil
	}
	environment, err := c.repositoryAuth.GitEnvironment(ctx, workspaceID, repoURL)
	if err != nil {
		return nil, nil, err
	}
	total := 0
	for key, value := range environment {
		total += len(value)
		if !validRepositoryAuthKey(key) || len(value) > 16384 || total > 65536 {
			clear(environment)
			return nil, nil, errors.New("repository auth reply exceeded scope")
		}
	}
	return context.WithValue(ctx, repositoryAuthContextKey{}, environment), func() { clear(environment) }, nil
}

func gitEnvironmentForContext(ctx context.Context) []string {
	auth, scoped := ctx.Value(repositoryAuthContextKey{}).(map[string]string)
	if !scoped {
		return gitEnv()
	}
	environment := GitEnvironment(os.Environ())
	for key, value := range auth {
		environment = append(environment, key+"="+value)
	}
	return gitEnvironmentWithSafeDirectory(environment)
}

func redactRepositoryAuthOutput(ctx context.Context, output []byte) []byte {
	auth, _ := ctx.Value(repositoryAuthContextKey{}).(map[string]string)
	for _, value := range auth {
		if value != "" {
			output = bytes.ReplaceAll(output, []byte(value), []byte("[redacted git credential]"))
		}
	}
	return output
}
