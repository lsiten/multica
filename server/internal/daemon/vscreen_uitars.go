package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/computeruse"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (e *vscreenExecution) invokeUITARS(ctx context.Context, args vscreenToolArgs) ([]map[string]any, error) {
	if e.uiTars == nil || args.Goal == "" || args.TransactionID == "" {
		return nil, errors.New("UI-TARS fallback is not configured")
	}
	observeArgs := vscreenToolArgs{TransactionID: args.TransactionID, WindowHandle: args.WindowHandle}
	var content []map[string]any
	var err error
	if e.physical != nil {
		content, err = e.invokePhysical(ctx, "vscreen_observe", observeArgs)
	} else {
		content, err = e.invoke(ctx, "vscreen_observe", mustJSONRaw(observeArgs))
	}
	if err != nil {
		return nil, err
	}
	var pngData []byte
	width, height, revision := 0, 0, uint64(0)
	for _, block := range content {
		if block["type"] == "image" {
			pngData, _ = base64.StdEncoding.DecodeString(fmt.Sprint(block["data"]))
		}
		if block["type"] == "text" {
			var meta map[string]any
			if json.Unmarshal([]byte(fmt.Sprint(block["text"])), &meta) == nil {
				if v, ok := meta["width"].(float64); ok {
					width = int(v)
				}
				if v, ok := meta["height"].(float64); ok {
					height = int(v)
				}
				if v, ok := meta["snapshot_revision"].(float64); ok {
					revision = uint64(v)
				}
			}
		}
	}
	if len(pngData) == 0 {
		return nil, errors.New("UI-TARS observation has no screenshot")
	}
	if width <= 0 || height <= 0 {
		return nil, errors.New("UI-TARS observation has invalid dimensions")
	}
	if revision > 0 {
		args.SnapshotRevision = revision
	}
	output, err := e.uiTars.Predict(ctx, args.Goal, pngData)
	if err != nil {
		return nil, err
	}
	decision, err := computeruse.ParseAction(output, width, height)
	if err != nil {
		return nil, err
	}
	if decision.Kind == "wait" || decision.Kind == "finished" {
		return vscreenText(map[string]any{"decision": decision.Kind}), nil
	}
	var action protocol.VscreenAction
	switch decision.Kind {
	case "click":
		action = protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &protocol.VscreenPoint{X: decision.X, Y: decision.Y}}}
	case "type":
		action = protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: args.WindowHandle, Text: decision.Text}}
	case "key":
		action = protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: decision.Key, Modifiers: decision.Modifiers}}
	case "scroll":
		action = protocol.VscreenAction{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{Position: protocol.VscreenPoint{X: decision.X, Y: decision.Y}, DeltaX: decision.DX, DeltaY: decision.DY}}
	default:
		return nil, errors.New("unsupported UI-TARS action")
	}
	sequence := args.Sequence
	if sequence == 0 {
		sequence = 1
	}
	actionID := args.ActionID
	if actionID == "" {
		actionID = "ui-tars"
	}
	return e.invoke(ctx, "vscreen_click", mustJSONRaw(vscreenToolArgs{TransactionID: args.TransactionID, WindowHandle: args.WindowHandle, SnapshotRevision: args.SnapshotRevision, ActionID: actionID, Sequence: sequence, Action: &action}))
}

func mustJSONRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
