package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
)

func normalizeRepositoryAuthURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("invalid repository identity")
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("repository auth does not accept URL parameters")
		}
		if parsed.User != nil {
			if _, secret := parsed.User.Password(); secret {
				return "", errors.New("repository credentials cannot be embedded in URL")
			}
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		return strings.TrimSuffix(parsed.String(), "/"), nil
	}
	// Local paths and scp-style SSH identities remain exact; SSH uses the
	// explicitly allowed agent/helper settings, not GitHub HTTP token variables.
	return raw, nil
}

func (c *environmentFactCallback) gitAuth(ctx context.Context, workspaceID, repoURL string) environmentFactReply {
	denied := environmentFactReply{Status: 403, Body: json.RawMessage(`{"error":"repository authorization unavailable"}`)}
	normalized, err := normalizeRepositoryAuthURL(repoURL)
	if err != nil || workspaceID == "" {
		return denied
	}
	matches := func(candidate string) bool {
		value, err := normalizeRepositoryAuthURL(candidate)
		return err == nil && value == normalized
	}
	c.daemon.mu.Lock()
	workspace := c.daemon.workspaces[workspaceID]
	bound := false
	var tasks []string
	runtimes := map[string]bool{}
	if workspace != nil {
		for candidate := range workspace.allowedRepoURLs {
			if matches(candidate) {
				bound = true
			}
		}
		for candidate := range workspace.taskRepoURLs {
			if matches(candidate) {
				bound = true
			}
		}
		for id, refs := range workspace.taskRepoRefs {
			for candidate := range refs {
				if matches(candidate) {
					tasks = append(tasks, id)
					break
				}
			}
		}
		for _, id := range workspace.runtimeIDs {
			runtimes[id] = true
		}
	}
	c.daemon.mu.Unlock()
	if !bound {
		return denied
	}
	current, err := c.daemon.client.GetWorkspaceRepos(ctx, workspaceID)
	if err != nil || current.WorkspaceID != workspaceID {
		return denied
	}
	allowed := false
	for _, repo := range current.Repos {
		if matches(repo.URL) {
			allowed = true
			break
		}
	}
	if !allowed && len(tasks) > 0 && len(tasks) <= 500 {
		statuses, err := c.daemon.client.GetTaskGCChecks(ctx, workspaceID, "", tasks)
		if err != nil {
			return denied
		}
		for _, status := range statuses {
			if status != nil && !status.Missing && status.Status == "running" && runtimes[status.RuntimeID] {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return denied
	}
	environment := map[string]string{}
	parsed, err := url.Parse(normalized)
	if err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		total := 0
		for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
			if value, exists := os.LookupEnv(key); exists {
				total += len(value)
				if len(value) > 16384 || total > 65536 {
					return denied
				}
				environment[key] = value
			}
		}
	}
	encoded, err := json.Marshal(environment)
	clear(environment)
	if err != nil {
		return denied
	}
	return environmentFactReply{Status: 200, Body: encoded}
}

type environmentRepositoryAuth struct{ facts *environmentFactTransport }

func (a environmentRepositoryAuth) GitEnvironment(ctx context.Context, workspaceID, repoURL string) (map[string]string, error) {
	reply, err := a.facts.fetch(ctx, environmentFact{Kind: "git_auth", WorkspaceID: workspaceID, RepoURL: repoURL})
	if err != nil {
		return nil, err
	}
	defer clear(reply.Body)
	if reply.Status != 200 {
		return nil, errors.New("repository authorization unavailable")
	}
	var environment map[string]string
	if err = json.Unmarshal(reply.Body, &environment); err != nil {
		return nil, errors.New("repository auth reply malformed")
	}
	return environment, nil
}
