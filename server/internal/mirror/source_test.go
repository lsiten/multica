package mirror

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"
)

type testCapturer struct {
	image image.Image
	err   error
}

func (c testCapturer) Capture(context.Context) (image.Image, error) {
	return c.image, c.err
}

type countingCapturer struct {
	image image.Image

	mu    sync.Mutex
	count int
}

type blockingCapturer struct {
	started chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func (c *blockingCapturer) Capture(ctx context.Context) (image.Image, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.stop:
		close(c.done)
		return nil, context.Canceled
	case <-ctx.Done():
		close(c.done)
		return nil, ctx.Err()
	}
}

func (c *countingCapturer) Capture(context.Context) (image.Image, error) {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	return c.image, nil
}

func (c *countingCapturer) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

type testSink struct {
	frames chan Frame
	errors chan ControlMessage
}

func (s testSink) SendFrame(frame Frame) error {
	s.frames <- frame
	return nil
}

func (s testSink) SendControl(message ControlMessage) error {
	if s.errors != nil {
		s.errors <- message
	}
	return nil
}

func TestSourceReportsTerminalCaptureErrorToViewers(t *testing.T) {
	// Given
	source := NewSource(
		testCapturer{err: ErrCapturePermissionDenied},
		time.Hour,
	)
	sink := testSink{
		frames: make(chan Frame, 1),
		errors: make(chan ControlMessage, 1),
	}
	remove := source.AddViewer("viewer-1", sink)
	defer remove()

	// When
	err := source.captureAndBroadcast(context.Background())

	// Then
	if !errors.Is(err, ErrCapturePermissionDenied) {
		t.Fatalf("capture error = %v, want %v", err, ErrCapturePermissionDenied)
	}
	select {
	case message := <-sink.errors:
		if message.Type != ControlMessageTypeError || message.Reason != ControlReasonPermissionDenied {
			t.Fatalf("control = %+v, want mirror:error permission-denied", message)
		}
	case <-time.After(time.Second):
		t.Fatal("viewer did not receive a capture error control")
	}
	source.mu.Lock()
	viewerCount := len(source.viewers)
	source.mu.Unlock()
	if viewerCount != 0 {
		t.Fatalf("viewer count = %d, want 0 after terminal capture error", viewerCount)
	}
}

func TestSourceReportsCaptureFailureToHandler(t *testing.T) {
	// Given
	cause := ErrCapturePermissionDenied
	source := NewSource(testCapturer{err: cause}, time.Hour)
	failures := make(chan error, 1)
	source.SetCaptureFailureHandler(func(err error) { failures <- err })
	sink := testSink{frames: make(chan Frame, 1), errors: make(chan ControlMessage, 1)}
	source.AddViewer("viewer-1", sink)

	// When
	err := source.captureAndBroadcast(context.Background())

	// Then
	if !errors.Is(err, cause) {
		t.Fatalf("capture error = %v, want %v", err, cause)
	}
	select {
	case got := <-failures:
		if !errors.Is(got, cause) || ControlReasonFromError(got) != ControlReasonPermissionDenied {
			t.Fatalf("failure callback error = %v, reason = %q", got, ControlReasonFromError(got))
		}
	case <-time.After(time.Second):
		t.Fatal("capture failure handler was not invoked")
	}
}

func TestSourceDoesNotReportContextCancellationAsCaptureError(t *testing.T) {
	// Given
	source := NewSource(
		testCapturer{err: context.Canceled},
		time.Hour,
	)
	sink := testSink{
		frames: make(chan Frame, 1),
		errors: make(chan ControlMessage, 1),
	}
	remove := source.AddViewer("viewer-1", sink)
	defer remove()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	err := source.captureAndBroadcast(ctx)

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("capture error = %v, want context.Canceled", err)
	}
	select {
	case message := <-sink.errors:
		t.Fatalf("context cancellation sent capture control %+v", message)
	default:
	}
}

func TestSourceDeliversFramesWhenViewerAttached(t *testing.T) {
	// Given
	image := image.NewRGBA(image.Rect(0, 0, 2, 2))
	image.Set(0, 0, color.White)
	source := NewSource(testCapturer{image: image}, 5*time.Millisecond)
	sink := testSink{frames: make(chan Frame, 1)}

	// When
	remove := source.AddViewer("viewer-1", sink)
	defer remove()

	// Then
	select {
	case frame := <-sink.frames:
		if frame.Width != 2 || frame.Height != 2 {
			t.Fatalf("frame dimensions = %dx%d, want 2x2", frame.Width, frame.Height)
		}
		if len(frame.JPEG) == 0 {
			t.Fatal("frame did not contain JPEG data")
		}
	case <-time.After(time.Second):
		t.Fatal("source did not deliver a frame")
	}
}

func TestSourceStopsWhenLastViewerLeaves(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	remove := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})

	// When
	remove()

	// Then
	if err := source.Close(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("close source: %v", err)
	}
}

func TestSourceRestartsCaptureAfterLastViewerLeaves(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	first := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	source.mu.Lock()
	firstDone := source.done
	source.mu.Unlock()

	// When
	first()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first capture loop did not stop")
	}
	second := source.AddViewer("viewer-2", testSink{frames: make(chan Frame, 1)})
	defer second()
	source.mu.Lock()
	secondDone := source.done
	source.mu.Unlock()

	// Then
	if secondDone == nil || secondDone == firstDone {
		t.Fatal("viewer restart did not create exactly one new capture loop")
	}
	if err := source.Close(context.Background()); err != nil {
		t.Fatalf("close source: %v", err)
	}
}

func TestSourceCloseIsIdempotent(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	remove := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	defer remove()

	// When
	if err := source.Close(context.Background()); err != nil {
		t.Fatalf("first close source: %v", err)
	}

	// Then
	if err := source.Close(context.Background()); err != nil {
		t.Fatalf("second close source: %v", err)
	}
}

func TestSourceRejectsDuplicateViewerWithoutReplacingExistingSink(t *testing.T) {
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	t.Cleanup(func() {
		if err := source.Close(context.Background()); err != nil {
			t.Fatalf("close source: %v", err)
		}
	})
	first := testSink{frames: make(chan Frame, 2)}
	second := testSink{frames: make(chan Frame, 2)}
	removeFirst := source.AddViewer("viewer-1", first)
	defer removeFirst()
	removeSecond := source.AddViewer("viewer-1", second)
	defer removeSecond()

	source.captureAndBroadcast(context.Background())
	select {
	case <-first.frames:
	default:
		t.Fatal("existing viewer did not receive the frame")
	}
	select {
	case <-second.frames:
		t.Fatal("duplicate viewer replaced the existing sink")
	default:
	}
}

func TestSourceCanonicalizesViewerIDsForDuplicateProtection(t *testing.T) {
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	t.Cleanup(func() {
		if err := source.Close(context.Background()); err != nil {
			t.Fatalf("close source: %v", err)
		}
	})
	first := testSink{frames: make(chan Frame, 2)}
	second := testSink{frames: make(chan Frame, 2)}
	removeFirst := source.AddViewer(" viewer-1 ", first)
	defer removeFirst()
	removeSecond := source.AddViewer("viewer-1", second)
	defer removeSecond()

	source.captureAndBroadcast(context.Background())
	select {
	case <-first.frames:
	default:
		t.Fatal("canonical viewer did not receive the frame")
	}
	select {
	case <-second.frames:
		t.Fatal("trimmed duplicate viewer replaced the existing sink")
	default:
	}
}

func TestSourceSharesOneCaptureLoopAcrossViewers(t *testing.T) {
	capturer := &countingCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	source := NewSource(capturer, 5*time.Millisecond)
	first := testSink{frames: make(chan Frame, 8)}
	second := testSink{frames: make(chan Frame, 8)}
	removeFirst := source.AddViewer("viewer-1", first)
	removeSecond := source.AddViewer("viewer-2", second)
	defer removeFirst()
	defer removeSecond()

	deadline := time.After(time.Second)
	for first.Count() < 2 || second.Count() < 2 {
		select {
		case <-deadline:
			t.Fatalf("viewers did not receive shared frames: first=%d second=%d", first.Count(), second.Count())
		case <-time.After(time.Millisecond):
		}
	}
	if count := capturer.Count(); count < 2 || count > 4 {
		t.Fatalf("capture count = %d, want one shared cadence", count)
	}
}

func TestSourceReportsViewerStateTransitions(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	changes := make(chan ViewerStateChange, 4)
	source.SetViewerStateHook(func(change ViewerStateChange) { changes <- change }, 1)

	// When
	first := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	second := source.AddViewer("viewer-2", testSink{frames: make(chan Frame, 1)})
	second()
	first()

	// Then
	expect := func(want ViewerStateChange) {
		select {
		case got := <-changes:
			if got != want {
				t.Fatalf("viewer state = %+v, want %+v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("viewer state %+v was not reported", want)
		}
	}
	expect(ViewerStateChange{ViewerID: "viewer-1", Active: true})
	expect(ViewerStateChange{ViewerID: "viewer-2", Active: true})
	expect(ViewerStateChange{ViewerID: "viewer-2", Active: false})
	expect(ViewerStateChange{ViewerID: "viewer-1", Active: false})
}

func TestSourceSetViewerStateHookReplaysActiveState(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	source.AddViewer("viewer-2", testSink{frames: make(chan Frame, 1)})
	source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	changes := make(chan ViewerStateChange, 2)

	// When
	active := source.SetViewerStateHook(func(change ViewerStateChange) { changes <- change }, 1)

	// Then
	if !active {
		t.Fatal("SetViewerStateHook active = false, want true")
	}
	if !source.HasViewers() {
		t.Fatal("HasViewers = false, want true")
	}
	expect := func(want ViewerStateChange) {
		t.Helper()
		select {
		case got := <-changes:
			if got != want {
				t.Fatalf("replayed viewer state = %+v, want %+v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("viewer state %+v was not replayed", want)
		}
	}
	expect(ViewerStateChange{ViewerID: "viewer-1", Active: true})
	expect(ViewerStateChange{ViewerID: "viewer-2", Active: true})
}

func TestSourceRejectsStaleViewerStateHook(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	current := make(chan ViewerStateChange, 1)
	stale := make(chan ViewerStateChange, 1)
	source.SetViewerStateHook(func(change ViewerStateChange) { current <- change }, 2)

	// When
	installed := source.SetViewerStateHook(func(change ViewerStateChange) { stale <- change }, 1)
	remove := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	defer remove()

	// Then
	if installed {
		t.Fatal("stale viewer-state hook replaced the current control connection")
	}
	select {
	case got := <-current:
		if got != (ViewerStateChange{ViewerID: "viewer-1", Active: true}) {
			t.Fatalf("current change = %+v, want viewer active", got)
		}
	default:
		t.Fatal("current viewer-state hook did not receive the active viewer")
	}
	select {
	case got := <-stale:
		t.Fatalf("stale hook received viewer change %+v", got)
	default:
	}
}

func TestSourceStaleDetachDoesNotRemoveReplacementViewer(t *testing.T) {
	// Given
	source := NewSource(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	first := testSink{frames: make(chan Frame, 2)}
	second := testSink{frames: make(chan Frame, 2)}
	removeFirst := source.AddViewer("viewer-1", first)
	removeFirst()
	removeSecond := source.AddViewer("viewer-1", second)

	// When
	removeFirst()
	source.captureAndBroadcast(context.Background())

	// Then
	select {
	case <-second.frames:
	default:
		t.Fatal("stale detach removed replacement viewer")
	}

	removeSecond()
	if err := source.Close(context.Background()); err != nil {
		t.Fatalf("close source: %v", err)
	}
}

func TestSourceCloseWaitsForCaptureLoop(t *testing.T) {
	// Given
	capturer := &blockingCapturer{
		started: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	source := NewSource(capturer, time.Hour)
	remove := source.AddViewer("viewer-1", testSink{frames: make(chan Frame, 1)})
	defer remove()
	<-capturer.started

	// When
	closeDone := make(chan error, 1)
	go func() { closeDone <- source.Close(context.Background()) }()
	close(capturer.stop)

	// Then
	select {
	case <-capturer.done:
	case <-time.After(time.Second):
		t.Fatal("capture loop did not observe cancellation")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close source: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wait for capture loop")
	}
}

func (s testSink) Count() int {
	return len(s.frames)
}
