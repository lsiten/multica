package jevmodels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/internal/daemon/processtree"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type modelProcess struct {
	endpoint        string
	token           string
	device          string
	requestedDevice string
	cancel          context.CancelFunc
	done            chan struct{}
	err             error
}

func (p *modelProcess) stop() error {
	p.cancel()
	<-p.done
	if errors.Is(p.err, processtree.ErrCleanup) {
		return p.err
	}
	return nil
}

func (m *Manager) start(ctx context.Context, s Selection) (*modelProcess, error) {
	if err := verifyFiles(ctx, m.modelDir(), m.files); err != nil {
		return nil, err
	}
	worker := filepath.Join(m.cfg.RootDir, "worker.py")
	if err := os.WriteFile(worker, workerSource, 0600); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err = ln.Close(); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(m.ctx)
	p := &modelProcess{endpoint: "http://127.0.0.1:" + strconv.Itoa(port), token: hex.EncodeToString(secret), requestedDevice: s.Device, cancel: cancel, done: make(chan struct{})}
	log, err := os.OpenFile(filepath.Join(m.cfg.RootDir, "service.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd := exec.Command(m.python(), "-I", worker)
	cmd.Env = append(cleanEnvironment(), "DECIDER_MODEL="+m.modelDir(), "DECIDER_DEVICE="+s.Device, "DECIDER_WARMUP=0", "DECIDER_COMPILE=0", "DECIDER_FP8=0", "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "MULTICA_JEV_TOKEN="+p.token, "MULTICA_JEV_PORT="+strconv.Itoa(port))
	cmd.Stdout = log
	cmd.Stderr = log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		log.Close()
		return nil, err
	}
	started := make(chan struct{})
	go func() {
		p.err = errors.Join(processtree.RunWithStart(processCtx, cmd, time.Second, func() error { close(started); return nil }), stdin.Close(), log.Close())
		close(p.done)
	}()
	select {
	case <-started:
	case <-p.done:
		cancel()
		return nil, p.err
	case <-ctx.Done():
		return nil, errors.Join(ctx.Err(), p.stop())
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, m.cfg.ReadyTimeout)
	defer readyCancel()
	if err = p.waitReady(readyCtx); err != nil {
		return nil, errors.Join(err, p.stop())
	}
	return p, nil
}

func (p *modelProcess) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("model readiness: %w", ctx.Err())
		case <-p.done:
			return fmt.Errorf("model worker exited before ready (see service.log): %w", p.err)
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"/health", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+p.token)
			res, err := client.Do(req)
			if err != nil {
				continue
			}
			var health struct {
				OK     bool   `json:"ok"`
				Device string `json:"device"`
			}
			err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&health)
			res.Body.Close()
			if err == nil && res.StatusCode == http.StatusOK && health.OK && health.Device != "" {
				p.device = health.Device
				return nil
			}
		}
	}
}

func (m *Manager) detectExitLocked() {
	if m.proc == nil {
		return
	}
	select {
	case <-m.proc.done:
		m.status.State = "failed"
		m.status.Error = "model service exited; see service.log"
	default:
	}
}
