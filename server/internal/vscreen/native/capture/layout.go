package capture

import "math"

// Layout relates full display points to letterboxed capture output pixels.
type Layout struct {
	SourceWidth   float64 `json:"source_width"`
	SourceHeight  float64 `json:"source_height"`
	OutputWidth   uint32  `json:"output_width"`
	OutputHeight  uint32  `json:"output_height"`
	ContentX      float64 `json:"content_x"`
	ContentY      float64 `json:"content_y"`
	ContentWidth  float64 `json:"content_width"`
	ContentHeight float64 `json:"content_height"`
}

// FitLayout mirrors the explicit SCStream source/destination rectangle calculation.
func FitLayout(sourceWidth, sourceHeight float64, width, height uint32) (Layout, error) {
	if sourceWidth <= 0 || sourceHeight <= 0 || math.IsNaN(sourceWidth) || math.IsNaN(sourceHeight) || math.IsInf(sourceWidth, 0) || math.IsInf(sourceHeight, 0) || width == 0 || height == 0 {
		return Layout{}, ErrConfig
	}
	factor := math.Min(float64(width)/sourceWidth, float64(height)/sourceHeight)
	contentWidth, contentHeight := sourceWidth*factor, sourceHeight*factor
	return Layout{sourceWidth, sourceHeight, width, height, (float64(width) - contentWidth) / 2, (float64(height) - contentHeight) / 2, contentWidth, contentHeight}, nil
}
