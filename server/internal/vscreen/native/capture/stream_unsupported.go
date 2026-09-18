//go:build !darwin || !cgo

package capture

import "context"

type Stream struct{}

func open(context.Context, Config) (*Stream, error)  { return nil, ErrUnsupported }
func (*Stream) Next(context.Context) (Sample, error) { return Sample{}, ErrUnsupported }
func (*Stream) ForceKeyframe(context.Context) error  { return ErrUnsupported }
func (*Stream) Close(context.Context) error          { return ErrUnsupported }

func (*Stream) Stats() (Stats, error) { return Stats{}, ErrUnsupported }

func PermissionGranted() (bool, error) { return false, ErrUnsupported }

func (*Stream) UpdateExclusions(context.Context, []uint32) error { return ErrUnsupported }
