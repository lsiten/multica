package appcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// InstalledApp identifies an NSWorkspace-resolved app without exposing paths or PIDs.
type InstalledApp struct {
	BundleID string `json:"bundle_id"`
	Name     string `json:"name"`
	Running  bool   `json:"running"`
}

// AppList is bounded to standard Applications locations. Launch may also resolve
// known bundle IDs outside this inventory; presence is not input certification.
type AppList struct {
	Apps      []InstalledApp `json:"apps"`
	Truncated bool           `json:"truncated"`
}

// Validate rejects malformed or excessive native inventory before tool exposure.
func (a AppList) Validate() error {
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > 48*1024 {
		return refusal("invalid_app_list")
	}
	if len(a.Apps) > 512 {
		return refusal("invalid_app_list")
	}
	seen := make(map[string]bool, len(a.Apps))
	for _, app := range a.Apps {
		if app.BundleID == "" || len(app.BundleID) > 255 || !utf8.ValidString(app.BundleID) || strings.ContainsAny(app.BundleID, "\x00\r\n") || app.Name == "" || len(app.Name) > 1024 || !utf8.ValidString(app.Name) || seen[app.BundleID] {
			return refusal("invalid_app_list")
		}
		seen[app.BundleID] = true
	}
	return nil
}

// ListApps discovers installed app identifiers without launching or activating any app.
func (c *Controller) ListApps(ctx context.Context, a Authority) (AppList, error) {
	ctx, leave, err := c.enter(ctx, a.Resource)
	if err != nil {
		return AppList{}, err
	}
	defer leave()
	if _, err = c.authorize(ctx, a, ControlAccess); err != nil {
		return AppList{}, err
	}
	list := AppList{Apps: []InstalledApp{}}
	if err = c.backend.call(ctx, "list_apps", nil, &list); err != nil {
		return AppList{}, err
	}
	if err = list.Validate(); err != nil {
		return AppList{}, err
	}
	return list, nil
}
