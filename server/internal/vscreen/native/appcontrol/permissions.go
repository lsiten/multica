package appcontrol

import "context"

// ProbePermissions uses only AXIsProcessTrusted and CGPreflightScreenCaptureAccess.
func ProbePermissions(ctx context.Context) (Permissions, error) {
	b, err := newBackend()
	if err != nil {
		return Permissions{}, err
	}
	defer b.close()
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	var permissions Permissions
	err = b.call(ctx, "probe", nil, &permissions)
	return permissions, err
}
