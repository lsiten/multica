//go:build !darwin

package nativecapture

import "context"

func Start(context.Context, Config) (*Client, error) {
	return nil, ErrUnsupported
}
