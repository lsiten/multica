package protocol

import (
	"fmt"
	"math"
)

const (
	EventMirrorSourcesQuery  = "mirror:sources-query"
	EventMirrorSourcesResult = "mirror:sources-result"
)

type MirrorSourceReason string

const (
	MirrorSourcePermissionDenied  MirrorSourceReason = "permission_denied"
	MirrorSourceGone              MirrorSourceReason = "source_gone"
	MirrorSourceNativeUnavailable MirrorSourceReason = "native_unavailable"
)

func (r MirrorSourceReason) Valid() bool {
	switch r {
	case MirrorSourcePermissionDenied, MirrorSourceGone, MirrorSourceNativeUnavailable:
		return true
	default:
		return false
	}
}

// MirrorSourcesQuery asks the current daemon connection for authorized,
// read-only physical and system sources.
type MirrorSourcesQuery struct {
	RuntimeEnvelope
}

// MirrorSourcesResult returns display metadata bound to one exact source grant.
type MirrorSourcesResult struct {
	RuntimeEnvelope
	Sources []MirrorSourceDescriptor `json:"sources,omitempty"`
	Reason  MirrorSourceReason       `json:"reason,omitempty"`
}

func (r MirrorSourcesResult) Validate() error {
	if err := r.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	if r.Reason != "" {
		if !r.Reason.Valid() || len(r.Sources) != 0 {
			return ErrInvalidMirrorContract
		}
		return nil
	}
	if len(r.Sources) == 0 {
		return fmt.Errorf("%w: empty source catalog", ErrInvalidMirrorContract)
	}
	if len(r.Sources) > 64 {
		return ErrInvalidMirrorContract
	}
	first := r.Sources[0]
	seen := make(map[MirrorSource]bool, len(r.Sources))
	primary := false
	for _, source := range r.Sources {
		if !sameDisplayResource(first.Resource, source.Resource) || source.NativeEpoch != first.NativeEpoch {
			return fmt.Errorf("%w: mixed source catalog", ErrInvalidMirrorContract)
		}
		if seen[source.Source] || primary && source.Primary || source.Source.Kind == MirrorSourceKind("virtual") {
			return fmt.Errorf("%w: invalid source catalog entry", ErrInvalidMirrorContract)
		}
		seen[source.Source] = true
		primary = primary || source.Primary
		if len(source.Name) > 256 || source.Width < 0 || source.Width > 32768 ||
			source.Height < 0 || source.Height > 32768 ||
			source.LogicalWidth < 0 || source.LogicalWidth > 32768 ||
			source.LogicalHeight < 0 || source.LogicalHeight > 32768 ||
			source.X < -32768 || source.X > 32768 || source.Y < -32768 || source.Y > 32768 ||
			source.GeometryRevision == 0 || source.Scale <= 0 || source.Scale > 16 ||
			math.IsNaN(source.Scale) || math.IsInf(source.Scale, 0) ||
			math.IsNaN(source.LogicalWidth) || math.IsInf(source.LogicalWidth, 0) ||
			math.IsNaN(source.LogicalHeight) || math.IsInf(source.LogicalHeight, 0) {
			return fmt.Errorf("%w: invalid source geometry", ErrInvalidMirrorContract)
		}
		if err := source.Resource.Validate(); err != nil {
			return err
		}
		if err := source.Source.Validate(); err != nil {
			return err
		}
		if source.Resource.WorkspaceID != r.WorkspaceID || source.Resource.RuntimeID != r.RuntimeID ||
			!mirrorIdentity(source.NativeEpoch) || !mirrorIdentity(source.Generation) {
			return fmt.Errorf("%w: foreign source binding", ErrInvalidMirrorContract)
		}
	}
	return nil
}

func sameDisplayResource(a, b ResourceKey) bool {
	return a.BackendIdentity == b.BackendIdentity && a.WorkspaceID == b.WorkspaceID &&
		a.RuntimeID == b.RuntimeID && a.UID == b.UID
}
