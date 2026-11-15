package daemon

import (
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// mirrorOfferResource selects the display identity only from the current native catalog.
func mirrorOfferResource(runtime protocol.ResourceKey, sources []nativecapture.SourceDescriptor, grant protocol.MirrorViewerGrant) (protocol.ResourceKey, bool) {
	for _, source := range sources {
		if source.Source != grant.Source {
			continue
		}
		expected := runtime
		expected.DisplayID = source.DisplayID
		if source.DisplayID != 0 && source.Resource == expected {
			return source.Resource, true
		}
	}
	return protocol.ResourceKey{}, false
}
