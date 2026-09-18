package mirror

import "github.com/multica-ai/multica/server/pkg/protocol"

// VideoQuality describes negotiated encoding output; it does not resize or
// select the underlying display. The native adapter must enforce MaxLevelIDC.
type VideoQuality = protocol.MirrorVideoQuality

func negotiateVideoQuality(source EncodedSource, level uint8) EncodedSource {
	width, height, maxBitrate := 1600, 900, 20000000
	source.MaxLevelIDC = 40
	if level < 40 {
		width, height, maxBitrate = 1280, 720, 14000000
		source.MaxLevelIDC = 31
	}
	if source.Width > width || source.Height > height {
		if source.Width*height > source.Height*width {
			source.Height = source.Height * width / source.Width
			source.Width = width
		} else {
			source.Width = source.Width * height / source.Height
			source.Height = height
		}
	}
	source.Width -= source.Width % 2
	source.Height -= source.Height % 2
	source.FPS = min(source.FPS, 30)
	source.Bitrate = min(source.Bitrate, maxBitrate)
	return source
}

func (s EncodedSource) videoQuality() VideoQuality {
	return VideoQuality{Width: s.Width, Height: s.Height, FPS: s.FPS, Bitrate: s.Bitrate, MaxLevelIDC: s.MaxLevelIDC}
}
