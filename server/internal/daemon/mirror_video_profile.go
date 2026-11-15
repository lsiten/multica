package daemon

import (
	"math"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func mirrorVideoSelection(source nativecapture.SourceDescriptor, profile string) mirror.EncodedSource {
	maxWidth, maxHeight, fps, bitrate := 1600, 900, 30, 8_000_000
	switch profile {
	case protocol.MirrorVideoProfileClear:
		bitrate = 20_000_000
	case protocol.MirrorVideoProfileSaver:
		maxWidth, maxHeight, fps, bitrate = 960, 540, 20, 2_000_000
	case protocol.MirrorVideoProfileSmooth, protocol.MirrorVideoProfileAuto, "":
		maxWidth, maxHeight = 1280, 720
	}
	width, height := fitVideoDimensions(int(source.Width), int(source.Height), maxWidth, maxHeight)
	return mirror.EncodedSource{
		Binding:          source.MirrorSourceBinding,
		GeometryRevision: source.GeometryRevision,
		DisplayID:        source.DisplayID,
		Width:            width,
		Height:           height,
		FPS:              fps,
		Bitrate:          bitrate,
		ShowCursor:       true,
	}
}

func fitVideoDimensions(sourceWidth, sourceHeight, maxWidth, maxHeight int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return maxWidth, maxHeight
	}
	scale := math.Min(float64(maxWidth)/float64(sourceWidth), float64(maxHeight)/float64(sourceHeight))
	width := min(sourceWidth, max(2, int(math.Round(float64(sourceWidth)*scale))))
	height := min(sourceHeight, max(2, int(math.Round(float64(sourceHeight)*scale))))
	width -= width % 2
	height -= height % 2
	return max(2, width), max(2, height)
}
