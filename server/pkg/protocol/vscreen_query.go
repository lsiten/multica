package protocol

import (
	"fmt"
	"math"
)

const (
	EventVscreenQuery       = "vscreen:query"
	EventVscreenQueryResult = "vscreen:query-result"
	EventMirrorViewerRenew  = "mirror:viewer-renew"
)

// VscreenQuery requests a current native observation over an authenticated connection.
type VscreenQuery struct {
	VscreenEnvelope
	Kind string `json:"kind"`
}

// VscreenQueryResult returns only observations authorized for the requested runtime.
type VscreenQueryResult struct {
	VscreenEnvelope
	State   *VscreenStateSnapshot     `json:"state,omitempty"`
	Sources []VscreenSourceDescriptor `json:"sources,omitempty"`
	Reason  VscreenRejectionReason    `json:"reason,omitempty"`
}

// Validate checks the response boundary before it reaches a waiting HTTP request.
func (r VscreenQueryResult) Validate(kind string) error {
	if err := r.VscreenEnvelope.Validate(); err != nil {
		return err
	}
	if r.Reason != "" {
		if !r.Reason.Valid() {
			return ErrInvalidVscreenContract
		}
		return nil
	}
	switch kind {
	case "state":
		if r.State == nil || r.State.RuntimeID != r.RuntimeID || len(r.Sources) != 0 {
			return ErrInvalidVscreenContract
		}
		return r.State.Validate()
	case "sources":
		if len(r.Sources) > 64 || r.State != nil {
			return ErrInvalidVscreenContract
		}
		seen := make(map[MirrorSource]bool, len(r.Sources))
		primary := false
		for _, source := range r.Sources {
			if !sameDisplayResource(r.Sources[0].Resource, source.Resource) || source.NativeEpoch != r.Sources[0].NativeEpoch {
				return ErrInvalidVscreenContract
			}
			if seen[source.Source] || (primary && source.Primary) {
				return ErrInvalidVscreenContract
			}
			seen[source.Source] = true
			primary = primary || source.Primary
			if len(source.Name) > 256 || source.Width < 0 || source.Width > 32768 || source.Height < 0 || source.Height > 32768 ||
				source.LogicalWidth < 0 || source.LogicalWidth > 32768 || source.LogicalHeight < 0 || source.LogicalHeight > 32768 ||
				source.X < -32768 || source.X > 32768 || source.Y < -32768 || source.Y > 32768 ||
				source.GeometryRevision == 0 || source.Scale < 0 || source.Scale > 16 || math.IsNaN(source.Scale) || math.IsInf(source.Scale, 0) ||
				math.IsNaN(source.LogicalWidth) || math.IsInf(source.LogicalWidth, 0) || math.IsNaN(source.LogicalHeight) || math.IsInf(source.LogicalHeight, 0) {
				return ErrInvalidVscreenContract
			}
			if err := source.Resource.Validate(); err != nil {
				return err
			}
			if err := source.Source.Validate(); err != nil {
				return err
			}
			if source.Resource.WorkspaceID != r.WorkspaceID || source.Resource.RuntimeID != r.RuntimeID || !vscreenIdentity(source.NativeEpoch) || !vscreenIdentity(source.Generation) {
				return fmt.Errorf("%w: foreign source binding", ErrInvalidVscreenContract)
			}
		}
		return nil
	default:
		return ErrInvalidVscreenContract
	}
}

// MirrorViewerRenewPayload replaces an existing viewer's deadline on the same daemon connection.
type MirrorViewerRenewPayload struct {
	DaemonGeneration string            `json:"daemon_generation"`
	Grant            MirrorViewerGrant `json:"grant"`
}

// VscreenSourceDescriptor keeps display labels beside their exact authorization binding.
type VscreenSourceDescriptor struct {
	MirrorSourceBinding
	DisplayID        uint32  `json:"display_id"`
	Name             string  `json:"name"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	LogicalWidth     float64 `json:"logical_width"`
	LogicalHeight    float64 `json:"logical_height"`
	Scale            float64 `json:"scale"`
	X                int32   `json:"x"`
	Y                int32   `json:"y"`
	GeometryRevision uint64  `json:"geometry_revision"`
}

func sameDisplayResource(a, b ResourceKey) bool {
	return a.BackendIdentity == b.BackendIdentity && a.WorkspaceID == b.WorkspaceID &&
		a.RuntimeID == b.RuntimeID && a.UID == b.UID
}
