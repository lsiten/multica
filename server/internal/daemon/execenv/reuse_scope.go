package execenv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RepositoryScopeFingerprint excludes presentation text and normalizes order
// and JSON formatting. Repository refs and resource configuration remain part
// of the identity, so another checkout cannot inherit a prior working tree.
func RepositoryScopeFingerprint(repos []RepoContextForEnv, resources []ProjectResourceForEnv) (string, error) {
	items := make([]string, 0, len(repos)+len(resources))
	for _, repo := range repos {
		data, err := json.Marshal(struct{ URL, Ref string }{strings.TrimSpace(repo.URL), strings.TrimSpace(repo.Ref)})
		if err != nil {
			return "", err
		}
		items = append(items, "repo:"+string(data))
	}
	for _, resource := range resources {
		var ref any
		if len(resource.ResourceRef) > 0 {
			if !json.Valid(resource.ResourceRef) {
				return "", fmt.Errorf("invalid repository scope resource %s", resource.ID)
			}
			decoder := json.NewDecoder(bytes.NewReader(resource.ResourceRef))
			decoder.UseNumber()
			if err := decoder.Decode(&ref); err != nil {
				return "", fmt.Errorf("invalid repository scope resource %s: %w", resource.ID, err)
			}
		}
		data, err := json.Marshal(struct {
			ID, Type string
			Ref      any
		}{resource.ID, resource.ResourceType, ref})
		if err != nil {
			return "", err
		}
		items = append(items, "resource:"+string(data))
	}
	sort.Strings(items)
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
