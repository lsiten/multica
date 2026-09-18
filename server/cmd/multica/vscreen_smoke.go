package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type smokeNativeClient interface {
	Call(context.Context, native.Request) (native.Response, error)
	Sources(context.Context, protocol.ResourceKey) ([]native.SourceDescriptor, error)
	Close() error
}

type vscreenSmokeResult struct {
	Scenario     string                   `json:"scenario"`
	Version      string                   `json:"version"`
	Commit       string                   `json:"commit"`
	Executable   string                   `json:"executable"`
	Status       string                   `json:"status"`
	GUIExercised bool                     `json:"gui_exercised"`
	Display      *native.Display          `json:"display,omitempty"`
	Source       *native.SourceDescriptor `json:"source,omitempty"`
	Epoch        protocol.VscreenEpoch    `json:"epoch"`
	Disposed     bool                     `json:"disposed"`
	HostClosed   bool                     `json:"host_closed"`
	Video        *smokeVideoResult        `json:"video,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

type smokeVideoResult struct {
	Scope    string        `json:"scope"`
	Decoded  bool          `json:"decoded"`
	Artifact string        `json:"artifact"`
	SHA256   string        `json:"sha256"`
	Samples  []smokeSample `json:"samples"`
	Elapsed  int64         `json:"elapsed_ms"`
}

type smokeSample struct {
	PTS      int64 `json:"pts_ns"`
	Duration int64 `json:"duration_ns"`
	Bytes    int   `json:"bytes"`
	Keyframe bool  `json:"keyframe"`
	NALTypes []int `json:"nal_types"`
}

func runVscreenSmoke(out io.Writer, scenario, evidence string) error {
	result := vscreenSmokeResult{Scenario: scenario, Version: version, Commit: commit, Status: "blocked"}
	var err error
	result.Executable, err = os.Executable()
	if err == nil {
		err = executeVscreenSmoke(&result, evidence)
	}
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Status = "passed"
	}
	return errors.Join(err, json.NewEncoder(out).Encode(result))
}

func executeVscreenSmoke(result *vscreenSmokeResult, evidence string) error {
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return errors.New("gui_not_authorized")
	}
	if result.Scenario != "lifecycle" && result.Scenario != "source" && result.Scenario != "video" {
		return errors.New("scenario_not_implemented")
	}
	if !native.Supported() {
		return errors.New("native_unsupported")
	}
	if !filepath.IsAbs(result.Executable) || !filepath.IsAbs(evidence) {
		return errors.New("absolute_paths_required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	client, err := hostclient.Start(ctx, hostclient.Config{Executable: result.Executable, Build: version + "/" + commit, Media: true})
	if err != nil {
		return err
	}
	result.GUIExercised = true
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return errors.Join(err, client.Close())
	}
	key := protocol.ResourceKey{BackendIdentity: "https://vscreen-smoke.invalid", WorkspaceID: "smoke", RuntimeID: hex.EncodeToString(identity[:]), UID: uint32(os.Getuid())}
	return exerciseSmokeDisplay(ctx, client, key, result, func(source native.SourceDescriptor) (*smokeVideoResult, error) {
		return captureSmokeVideo(ctx, client, source, evidence)
	})
}

func exerciseSmokeDisplay(ctx context.Context, client smokeNativeClient, key protocol.ResourceKey, result *vscreenSmokeResult, video func(native.SourceDescriptor) (*smokeVideoResult, error)) (runErr error) {
	defer func() {
		closeErr := client.Close()
		result.HostClosed = closeErr == nil
		runErr = errors.Join(runErr, closeErr)
	}()
	created, err := client.Call(ctx, native.Request{Operation: "ensure", Resource: key, Width: 1280, Height: 720})
	if err != nil {
		return err
	}
	result.Display, result.Epoch = created.Display, created.Epoch
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if _, err := client.Call(cleanup, native.Request{Operation: "quiesce", Resource: key, Epoch: result.Epoch}); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("quiesce: %w", err))
			return
		}
		if _, err := client.Call(cleanup, native.Request{Operation: "dispose", Resource: key, Epoch: result.Epoch}); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("dispose: %w", err))
			return
		}
		listed, err := client.Call(cleanup, native.Request{Operation: "list"})
		if err != nil {
			runErr = errors.Join(runErr, err)
			return
		}
		for _, display := range listed.Displays {
			if result.Display != nil && display.ID == result.Display.ID {
				runErr = errors.Join(runErr, errors.New("disposed_display_still_present"))
				return
			}
		}
		result.Disposed = true
	}()
	if created.Display == nil || created.Display.ID == 0 || !created.Display.Managed || created.Epoch.Validate() != nil {
		return errors.New("invalid_created_display")
	}
	readback, err := client.Call(ctx, native.Request{Operation: "list"})
	if err != nil {
		return err
	}
	found := false
	for _, display := range readback.Displays {
		if display.ID == created.Display.ID && display.UUID == created.Display.UUID {
			found = true
		}
	}
	if !found {
		return errors.New("created_display_not_enumerated")
	}
	sources, err := client.Sources(ctx, key)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.DisplayID == created.Display.ID && source.Source.Kind == protocol.MirrorSourceVirtual && source.Resource == key && source.NativeEpoch == created.Epoch.NativeEpoch && source.Generation == created.Epoch.DisplayGeneration && source.GeometryRevision == created.Epoch.GeometryRevision {
			result.Source = &source
			break
		}
	}
	if result.Source == nil {
		return errors.New("created_display_missing_from_capture_sources")
	}
	if result.Scenario == "video" {
		result.Video, err = video(*result.Source)
		return err
	}
	return nil
}

func captureSmokeVideo(ctx context.Context, client *hostclient.Client, source native.SourceDescriptor, evidence string) (result *smokeVideoResult, runErr error) {
	stream, err := client.OpenStream(ctx, hostclient.EncodedSelection{Source: source, Width: 1280, Height: 720, FPS: 30, Bitrate: 4_000_000, MaxLevelIDC: 31})
	if err != nil {
		return nil, err
	}
	defer func() { runErr = errors.Join(runErr, stream.Close()) }()
	path := filepath.Join(evidence, "virtual-screen.h264")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { runErr = errors.Join(runErr, file.Close()) }()
	result = &smokeVideoResult{Artifact: path, Scope: "native-capture-encode"}
	digest := sha256.New()
	writer := io.MultiWriter(file, digest)
	started := time.Now()
	for index := 0; index < 3; index++ {
		sample, err := stream.Next(ctx)
		if err != nil {
			return result, err
		}
		types, err := smokeNALTypes(sample.AnnexB)
		if err != nil || sample.DurationNanos <= 0 || index == 0 && (!sample.KeyFrame || !containsNAL(types, 7) || !containsNAL(types, 8) || !containsNAL(types, 5)) || index > 0 && sample.PTSNanos <= result.Samples[index-1].PTS {
			return result, errors.New("invalid_h264_sample")
		}
		if _, err := writer.Write(sample.AnnexB); err != nil {
			return result, err
		}
		result.Samples = append(result.Samples, smokeSample{PTS: sample.PTSNanos, Duration: sample.DurationNanos, Bytes: len(sample.AnnexB), Keyframe: sample.KeyFrame, NALTypes: types})
	}
	result.Elapsed = time.Since(started).Milliseconds()
	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func containsNAL(types []int, expected int) bool {
	for _, kind := range types {
		if kind == expected {
			return true
		}
	}
	return false
}

func smokeNALTypes(data []byte) ([]int, error) {
	var types []int
	if len(data) < 5 || data[0] != 0 || data[1] != 0 || data[2] != 0 || data[3] != 1 {
		return nil, errors.New("missing_annex_b_prefix")
	}
	for index := 0; index+4 < len(data); index++ {
		if data[index] == 0 && data[index+1] == 0 && data[index+2] == 0 && data[index+3] == 1 {
			kind := data[index+4] & 31
			if kind == 0 || kind > 23 || data[index+4]&128 != 0 {
				return nil, errors.New("invalid_nal_header")
			}
			types = append(types, int(kind))
		}
	}
	if len(types) == 0 || (!containsNAL(types, 1) && !containsNAL(types, 5)) {
		return nil, errors.New("missing_h264_slice")
	}
	return types, nil
}
