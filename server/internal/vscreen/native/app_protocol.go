package native

import (
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AppRequest is private parent authority, never a tool-facing request. LeaseTTLMS
// is relative to host monotonic time and cannot exceed fifteen seconds.
type AppRequest struct {
	Authority    appcontrol.Authority           `json:"authority"`
	LeaseTTLMS   uint32                         `json:"lease_ttl_ms,omitempty"`
	Launch       *appcontrol.LaunchRequest      `json:"launch,omitempty"`
	Action       *protocol.VscreenActionRequest `json:"action,omitempty"`
	WindowHandle string                         `json:"window_handle,omitempty"`
	IncludePNG   bool                           `json:"include_png,omitempty"`
	SnapshotID   string                         `json:"snapshot_id,omitempty"`
	CancelID     string                         `json:"cancel_id,omitempty"`
	Human        *HumanGrant                    `json:"human,omitempty"`
}

// HumanGrant binds a separately minted Desktop-owner capability to one intervention,
// window and catalog destination. Only the authenticated daemon may issue it.
type HumanGrant struct {
	Capability          string `json:"capability"`
	InterventionID      string `json:"intervention_id"`
	WindowHandle        string `json:"window_handle"`
	Direction           string `json:"direction"`
	DestinationSourceID string `json:"destination_source_id"`
}

// SnapshotDescriptor binds PNG chunks to a single observation and opaque window.
type SnapshotDescriptor struct {
	ID               string                `json:"id"`
	Resource         protocol.ResourceKey  `json:"resource"`
	Epoch            protocol.VscreenEpoch `json:"epoch"`
	WindowHandle     string                `json:"window_handle"`
	SnapshotRevision uint64                `json:"snapshot_revision"`
	DisplayID        uint32                `json:"display_id"`
	Size             uint32                `json:"size"`
	SHA256           string                `json:"sha256"`
}

// AppResponse contains metadata only; PNG bytes travel as MediaSnapshot chunks.
type AppResponse struct {
	ManagedWindows *[]appcontrol.ManagedWindow  `json:"managed_windows,omitempty"`
	Candidates     *appcontrol.WindowCandidates `json:"candidates,omitempty"`
	Apps           *appcontrol.AppList          `json:"apps,omitempty"`
	Window         *appcontrol.Window           `json:"window,omitempty"`
	Observation    *appcontrol.Observation      `json:"observation,omitempty"`
	Snapshot       *SnapshotDescriptor          `json:"snapshot,omitempty"`
	Result         *appcontrol.Result           `json:"result,omitempty"`
	Permissions    *appcontrol.Permissions      `json:"permissions,omitempty"`
}
