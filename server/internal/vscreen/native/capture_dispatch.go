package native

import "github.com/multica-ai/multica/server/pkg/protocol"

func captureResponse(host *captureHost, catalog sourceCatalog, resources map[protocol.ResourceKey]resourceDisplay, nativeEpoch string, request Request, response Response) Response {
	var descriptor *CaptureDescriptor
	var err error
	switch request.Operation {
	case "sources", "start_capture":
		var displays []Display
		displays, err = listDisplays()
		if err != nil {
			response.Error = err.Error()
			return response
		}
		syncResourceGeometry(resources, displays)
		catalog.refresh(displays, nativeEpoch)
		sources, sourceErr := catalog.project(request.Resource, resources)
		if sourceErr != nil {
			response.Error = sourceErr.Error()
			return response
		}
		if request.Operation == "sources" {
			response.Sources = sources
			return response
		}
		descriptor, err = host.start(request, sources)
	case "stop_capture", "force_keyframe", "capture_status":
		descriptor, err = host.operate(request)
	default:
		response.Error = "operation_unsupported"
		return response
	}
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.Capture = descriptor
	response.Epoch = protocol.VscreenEpoch{NativeEpoch: descriptor.Source.NativeEpoch, DisplayGeneration: descriptor.Source.Generation, GeometryRevision: descriptor.Source.GeometryRevision}
	return response
}

func syncResourceGeometry(resources map[protocol.ResourceKey]resourceDisplay, displays []Display) {
	for key, resource := range resources {
		for _, display := range displays {
			if resource.display.ID == display.ID && resource.display.UUID == display.UUID && geometryChanged(resource.display, display) {
				resource.display = display
				resource.epoch.GeometryRevision++
				resource.quiescent = true
				resources[key] = resource
			}
		}
	}
}
func geometryChanged(before, after Display) bool {
	return before.X != after.X || before.Y != after.Y || before.Width != after.Width || before.Height != after.Height || before.Scale != after.Scale || before.LogicalWidth != after.LogicalWidth || before.LogicalHeight != after.LogicalHeight
}
