package computeruse

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Decision keeps inference separate from authority to execute an action.
// Coordinates refer to the unresized screenshot submitted for prediction.
type Decision struct {
	Kind      string
	X         float64
	Y         float64
	Text      string
	Key       string
	Modifiers []string
	DX        float64
	DY        float64
}

var clickAction = regexp.MustCompile(`^click\(start_box='\(([0-9]+(?:\.[0-9]+)?),\s*([0-9]+(?:\.[0-9]+)?)\)'\)$`)
var typeAction = regexp.MustCompile(`^type\(content='([^']*)'\)$`)
var hotkeyAction = regexp.MustCompile(`^hotkey\(key='([a-z0-9 ]+)'\)$`)
var scrollAction = regexp.MustCompile(`^scroll\(point='<point>([0-9]+(?:\.[0-9]+)?)\s+([0-9]+(?:\.[0-9]+)?)</point>', direction='(up|down|left|right)'\)$`)

// ParseAction accepts a bounded subset without evaluating generated code.
// Unsupported actions are errors, never interpreted as successful completion.
func ParseAction(output string, width, height int) (Decision, error) {
	if width <= 0 || height <= 0 {
		return Decision{}, errors.New("invalid screenshot dimensions")
	}
	parts := strings.Split(output, "Action:")
	if len(parts) != 2 {
		return Decision{}, errors.New("UI-TARS must return exactly one action")
	}
	action := strings.TrimSpace(parts[1])
	switch action {
	case "wait()":
		return Decision{Kind: "wait"}, nil
	case "finished()":
		return Decision{Kind: "finished"}, nil
	}
	if matches := clickAction.FindStringSubmatch(action); matches != nil {
		x, errX := strconv.ParseFloat(matches[1], 64)
		y, errY := strconv.ParseFloat(matches[2], 64)
		if errX != nil || errY != nil || math.IsInf(x, 0) || math.IsInf(y, 0) || x >= float64(width) || y >= float64(height) {
			return Decision{}, errors.New("UI-TARS point outside screenshot")
		}
		return Decision{Kind: "click", X: x, Y: y}, nil
	}
	if matches := typeAction.FindStringSubmatch(action); matches != nil && len(matches[1]) <= 8192 {
		return Decision{Kind: "type", Text: matches[1]}, nil
	}
	if matches := hotkeyAction.FindStringSubmatch(action); matches != nil {
		keys := strings.Fields(matches[1])
		if len(keys) == 0 || len(keys) > 3 {
			return Decision{}, errors.New("invalid UI-TARS hotkey")
		}
		return Decision{Kind: "key", Key: keys[len(keys)-1], Modifiers: keys[:len(keys)-1]}, nil
	}
	if matches := scrollAction.FindStringSubmatch(action); matches != nil {
		x, _ := strconv.ParseFloat(matches[1], 64)
		y, _ := strconv.ParseFloat(matches[2], 64)
		if x >= float64(width) || y >= float64(height) {
			return Decision{}, errors.New("UI-TARS point outside screenshot")
		}
		d := Decision{Kind: "scroll", X: x, Y: y}
		if matches[3] == "up" {
			d.DY = -600
		}
		if matches[3] == "down" {
			d.DY = 600
		}
		if matches[3] == "left" {
			d.DX = -600
		}
		if matches[3] == "right" {
			d.DX = 600
		}
		return d, nil
	}
	return Decision{}, fmt.Errorf("unsupported UI-TARS action")
}
