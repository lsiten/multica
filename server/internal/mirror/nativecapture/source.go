package nativecapture

import (
	"os"
	"sort"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type Display struct {
	ID            uint32  `json:"display_id"`
	UUID          string  `json:"uuid"`
	Name          string  `json:"name"`
	Builtin       bool    `json:"builtin"`
	LogicalWidth  float64 `json:"logical_width"`
	LogicalHeight float64 `json:"logical_height"`
	Scale         float64 `json:"scale"`
	Main          bool    `json:"main"`
	X             int32   `json:"x"`
	Y             int32   `json:"y"`
	Width         uint32  `json:"width"`
	Height        uint32  `json:"height"`
}

type sourceRecord struct {
	display Display
	epoch   Epoch
}

type sourceCatalog map[uint32]sourceRecord

func (c sourceCatalog) refresh(displays []Display, nativeEpoch string) {
	visible := make(map[uint32]bool, len(displays))
	for _, display := range displays {
		visible[display.ID] = true
		record, exists := c[display.ID]
		if !exists || record.display.UUID != display.UUID {
			record = sourceRecord{epoch: Epoch{NativeEpoch: nativeEpoch, DisplayGeneration: newEpoch(), GeometryRevision: 1}}
		} else if record.display.X != display.X || record.display.Y != display.Y ||
			record.display.Width != display.Width || record.display.Height != display.Height ||
			record.display.Scale != display.Scale {
			record.epoch.GeometryRevision++
		}
		record.display = display
		c[display.ID] = record
	}
	for id := range c {
		if !visible[id] {
			delete(c, id)
		}
	}
}

func (c sourceCatalog) project(key protocol.ResourceKey) ([]SourceDescriptor, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if key.UID != uint32(os.Getuid()) {
		return nil, ErrProtocol
	}
	sources := make([]SourceDescriptor, 0, len(c))
	for _, record := range c {
		display := record.display
		kind := protocol.MirrorSourceSystem
		if display.Builtin {
			kind = protocol.MirrorSourcePhysical
		}
		resource := key
		resource.DisplayID = display.ID
		sources = append(sources, SourceDescriptor{
			MirrorSourceBinding: protocol.MirrorSourceBinding{
				Resource:    resource,
				Source:      protocol.MirrorSource{Kind: kind, SourceID: "display:" + display.UUID},
				NativeEpoch: record.epoch.NativeEpoch,
				Generation:  record.epoch.DisplayGeneration,
				Primary:     display.Main,
			},
			DisplayID:        display.ID,
			Name:             display.Name,
			Width:            display.Width,
			Height:           display.Height,
			LogicalWidth:     display.LogicalWidth,
			LogicalHeight:    display.LogicalHeight,
			Scale:            display.Scale,
			X:                display.X,
			Y:                display.Y,
			GeometryRevision: record.epoch.GeometryRevision,
		})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].DisplayID < sources[j].DisplayID })
	return sources, nil
}
