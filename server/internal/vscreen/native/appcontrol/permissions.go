package appcontrol

import "context"

// ProbePermissions uses only non-prompting preflight checks.
func ProbePermissions(ctx context.Context) (Permissions, error) {
	return queryPermissions(ctx, "probe")
}

// RequestPermissions prompts through the selected Accessibility and Screen
// Recording TCC consent flows.
func RequestPermissions(ctx context.Context, request PermissionRequest) (Permissions, error) {
	if !request.Accessibility && !request.ScreenRecording {
		return Permissions{}, refusal("invalid_action")
	}
	return queryPermissions(ctx, "request_permissions", request)
}

func queryPermissions(ctx context.Context, operation string, request ...PermissionRequest) (Permissions, error) {
	b, err := newBackend()
	if err != nil {
		return Permissions{}, err
	}
	defer b.close()
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	var permissions Permissions
	var input any
	if len(request) == 1 {
		input = request[0]
	}
	err = b.call(ctx, operation, input, &permissions)
	return permissions, err
}
