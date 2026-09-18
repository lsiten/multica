package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type performanceNative interface {
	Call(context.Context, native.Request) (native.Response, error)
	Sources(context.Context, protocol.ResourceKey) ([]native.SourceDescriptor, error)
	Grant(context.Context, appcontrol.Authority, time.Duration) error
	ResumeApps(context.Context, appcontrol.Authority) error
	LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error)
	Revoke(context.Context, appcontrol.Authority) error
	OwnedPID() int
	Close() error
}
type performanceFixture interface {
	BeforeLaunch()
	Read() (smokefixture.State, error)
	Stop(context.Context) error
}
type performanceOwned struct {
	key                     protocol.ResourceKey
	epoch                   protocol.VscreenEpoch
	displayID               uint32
	fixture                 performanceFixture
	fixtureClosed, disposed bool
	marker                  *smokefixture.PerformanceMarker
}
type performancePeer struct {
	runtime     *mirror.RuntimeMirror
	negotiation mirror.Negotiation
	grant       protocol.MirrorViewerGrant
	sourceID    string
}
type performanceProducer struct {
	readNetworkRoute                   func(context.Context, mirror.SelectedICEPair) performanceRouteEvidence
	systemGPU                          performanceSystemGPU
	readGPU                            func() (performanceGPUSample, error)
	finishedOnce                       sync.Once
	journalBytes                       uint64
	peerStatsMissing                   bool
	mu                                 sync.Mutex
	config                             VscreenPerformanceSmokeConfig
	started                            time.Time
	now                                func() uint64
	readProcess                        func(int) (performanceProcessReading, error)
	client                             performanceNative
	capture                            *performanceCapture
	hub                                *mirror.CaptureHub
	runtimes                           map[string]*mirror.RuntimeMirror
	sources                            map[string]performanceSource
	owned                              []*performanceOwned
	peers                              map[string]performancePeer
	resources                          map[string]performanceResources
	processStarts                      map[string]uint64
	pid, nativePID                     int
	shared                             performanceShared
	cycles                             []performanceCycle
	errors                             []string
	closedPeerBytes                    uint64
	journal                            *os.File
	finished                           chan struct{}
	finishOnce                         sync.Once
	result                             performanceResult
	closed                             bool
	workspaceID, backendID, clockEpoch string
	prepareFixture                     func(uint32) (performanceFixture, string, error)
}

func validatePerformanceConfig(c VscreenPerformanceSmokeConfig, parent int) error {
	token, err := hex.DecodeString(c.Nonce)
	if c.SchemaVersion != 1 || err != nil || len(token) != 32 || c.ParentPID != parent || parent <= 0 || !filepath.IsAbs(c.EvidenceDir) || c.Requested != (VscreenPerformanceRequest{1600, 900, 30}) || c.Cycles < 0 || c.Cycles > 30 || c.DurationMS < 1000 || c.DurationMS > 1800000 {
		return errors.New("invalid_performance_config")
	}
	if c.Mode != "debug" && c.Mode != "acceptance" || c.Mode == "acceptance" && (c.DurationMS != 1800000 || c.Cycles != 30) {
		return errors.New("invalid_performance_budget")
	}
	return nil
}

// RunVscreenPerformanceSmoke owns one opt-in native performance run and a nonce-private HTTP surface.
func RunVscreenPerformanceSmoke(ctx context.Context, configPath, nativeBuild string, out io.Writer) (runErr error) {
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return errors.New("gui_not_authorized")
	}
	var cfg VscreenPerformanceSmokeConfig
	if err := readSmokePrivateJSON(configPath, &cfg); err != nil {
		return err
	}
	if err := validatePerformanceConfig(cfg, os.Getppid()); err != nil {
		return err
	}
	if !native.Supported() || performanceClockNS() == 0 {
		return errors.New("native_performance_unsupported")
	}
	if err := os.MkdirAll(cfg.EvidenceDir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(cfg.EvidenceDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid_evidence_directory")
	}
	if err = os.Chmod(cfg.EvidenceDir, 0700); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	baseURL := "http://" + listener.Addr().String()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.DurationMS)*time.Millisecond+15*time.Minute)
	defer cancel()
	gpu := performanceGPUBaseline(runCtx)
	client, err := hostclient.Start(runCtx, hostclient.Config{Executable: executable, Build: nativeBuild, Media: true, AppControl: true, ShutdownTimeout: 10 * time.Second})
	if err != nil {
		return err
	}
	producer := &performanceProducer{config: cfg, started: time.Now(), now: performanceClockNS, readProcess: performanceProcess, client: client, runtimes: map[string]*mirror.RuntimeMirror{}, sources: map[string]performanceSource{}, peers: map[string]performancePeer{}, resources: map[string]performanceResources{"go": unavailablePerformanceResources(), "native": unavailablePerformanceResources()}, processStarts: map[string]uint64{}, pid: os.Getpid(), nativePID: client.OwnedPID(), finished: make(chan struct{}), workspaceID: uuid.NewString(), backendID: baseURL, clockEpoch: uuid.NewString()}
	producer.systemGPU = gpu
	producer.readGPU = performanceGPU
	producer.errors = []string{}
	producer.cycles = []performanceCycle{}
	producer.capture = &performanceCapture{provider: VscreenCaptureProvider{Client: client}}
	producer.hub = mirror.NewCaptureHub(producer.capture)
	producer.prepareFixture = func(tag uint32) (performanceFixture, string, error) {
		app, e := smokefixture.PreparePerformance(executable, cfg.EvidenceDir, smokefixture.PerformanceMode{SourceTag: tag, LifetimeMS: cfg.DurationMS + 900000})
		if e != nil {
			return nil, "", e
		}
		return app, app.BundleID, nil
	}
	for name, pid := range map[string]int{"go": producer.pid, "native": producer.nativePID} {
		if value, e := performanceProcess(pid); e == nil {
			producer.processStarts[name] = value.Start
		}
	}
	defer func() {
		result := producer.finish(runErr)
		runErr = errors.Join(runErr, json.NewEncoder(out).Encode(result))
		if !result.CleanupConfirmed {
			runErr = errors.Join(runErr, errors.New("performance_cleanup_unconfirmed"))
		}
	}()
	producer.journal, err = os.OpenFile(filepath.Join(cfg.EvidenceDir, "performance-browser-samples.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	producer.sampleResources(runCtx)
	for range 2 {
		if err = producer.createSource(runCtx); err != nil {
			return err
		}
	}
	version, commit, _ := strings.Cut(nativeBuild, "/")
	ready := performanceReady{Mode: cfg.Mode, DurationMS: cfg.DurationMS, Cycles: cfg.Cycles, Type: "performance-ready", SchemaVersion: 1, Executable: executable, Version: version, Commit: commit, BaseURL: baseURL, HostClock: "mach_continuous_time_ns"}
	for _, source := range producer.sources {
		ready.Sources = append(ready.Sources, source)
	}
	path := filepath.Join(cfg.EvidenceDir, "performance-ready.json")
	ready.Nonce = cfg.Nonce
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = errors.Join(json.NewEncoder(file).Encode(ready), file.Close())
	if err != nil {
		return err
	}
	defer os.Remove(path)
	ready.Nonce = ""
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return runCtx }, Handler: producer, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second}
	served := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); served <- server.Serve(listener) }()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		shutdownErr := server.Shutdown(cleanup)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		<-serveDone
		runErr = errors.Join(runErr, shutdownErr)
	}()
	if err = json.NewEncoder(out).Encode(ready); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	producer.sampleResources(runCtx)
	for {
		select {
		case <-runCtx.Done():
			return runCtx.Err()
		case err = <-served:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case <-producer.finished:
			return nil
		case <-ticker.C:
			if os.Getppid() != cfg.ParentPID {
				return errors.New("performance_owner_gone")
			}
			producer.sampleResources(runCtx)
		}
	}
}
func performanceTag() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
