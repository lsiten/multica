package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type qualificationManualEvidence struct {
	Status        string `json:"status"`
	Source        string `json:"source"`
	KeyCount      uint64 `json:"key_count"`
	FirstKeyNS    int64  `json:"first_key_ns"`
	LastKeyNS     int64  `json:"last_key_ns"`
	FirstActionNS int64  `json:"first_action_ns"`
	LastActionNS  int64  `json:"last_action_ns"`
	TextSHA256    string `json:"text_sha256,omitempty"`
	UserConfirmed bool   `json:"user_confirmed"`
	ScratchClosed bool   `json:"scratch_closed"`
}
type qualificationManual struct {
	app    *smokefixture.App
	scope  smokefixture.QualificationScope
	result *qualificationManualEvidence
	read   func() (smokefixture.QualificationState, error)
	prompt func(int) error
	pid    int
	start  string
	window uint32
	exited <-chan error
}

func startQualificationManual(ctx context.Context, exe, evidence string, key protocol.ResourceKey, result *qualificationManualEvidence) (*qualificationManual, error) {
	app, scope, err := smokefixture.PrepareQualificationScratch(exe, evidence, key)
	if err != nil {
		return nil, err
	}
	m := &qualificationManual{app: app, scope: scope, result: result, read: scope.Read, prompt: scope.AdvanceQualificationPrompt}
	cmd := exec.Command(scope.Executable())
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "MULTICA_RUN_VSCREEN_GUI_SMOKE=1"}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = app.Stop(ctx)
		return nil, errors.New("manual_fixture_start_failed")
	}
	app.BeforeLaunch()
	m.pid = cmd.Process.Pid
	done := make(chan error, 1)
	m.exited = done
	go func() { done <- cmd.Wait() }()
	return m, nil
}
func (m *qualificationManual) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := m.app.Stop(ctx)
	if err != nil {
		return err
	}
	select {
	case e := <-m.exited:
		m.result.ScratchClosed = e == nil
		return e
	case <-ctx.Done():
		return errors.New("manual_fixture_cleanup_unconfirmed")
	}
}
func (m *qualificationManual) await(ctx context.Context, predicate func(smokefixture.QualificationState) bool) (smokefixture.QualificationState, error) {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		s, err := m.read()
		if err == nil && s.PID == m.pid && s.ProcessStart != "" && !s.Closed && s.WindowID != 0 {
			if m.start == "" {
				m.start = s.ProcessStart
				m.window = s.WindowID
			}
			if s.ProcessStart != m.start || s.WindowID != m.window {
				return s, errors.New("manual_fixture_identity_changed")
			}
			if predicate(s) {
				return s, nil
			}
		}
		select {
		case <-wait.Done():
			return s, errors.New("manual_input_unverified")
		case <-ticker.C:
		}
	}
}
func (m *qualificationManual) ready(ctx context.Context) error {
	if err := m.prompt(1); err != nil {
		return err
	}
	_, err := m.await(ctx, func(s smokefixture.QualificationState) bool {
		return s.ManualStarted && (s.Text == "" && s.KeyCount == 0 || s.Text == "A" && s.KeyCount == 1)
	})
	return err
}
func (m *qualificationManual) before(ctx context.Context, index int) error {
	if err := m.prompt(index + 1); err != nil {
		return err
	}
	expected := "ABCDE"[:index+1]
	_, err := m.await(ctx, func(s smokefixture.QualificationState) bool {
		return s.ManualStarted && s.Text == expected && s.KeyCount == uint64(index+1)
	})
	if err == nil && index == 0 {
		m.result.FirstActionNS = time.Now().UnixNano()
	}
	return err
}
func (m *qualificationManual) finish(ctx context.Context) error {
	m.result.LastActionNS = time.Now().UnixNano()
	if err := m.prompt(6); err != nil {
		return err
	}
	state, err := m.await(ctx, func(s smokefixture.QualificationState) bool { return s.ManualConfirmed })
	if err != nil {
		return err
	}
	return m.accept(state)
}
func (m *qualificationManual) accept(s smokefixture.QualificationState) error {
	sum := sha256.Sum256([]byte(s.Text))
	m.result.TextSHA256 = hex.EncodeToString(sum[:])
	m.result.KeyCount = s.KeyCount
	m.result.FirstKeyNS = s.FirstKeyNS
	m.result.LastKeyNS = s.LastKeyNS
	m.result.UserConfirmed = s.ManualConfirmed
	if !s.ManualStarted || !s.ManualConfirmed || s.Text != "ABCDEZ" || s.KeyCount != 6 || s.FirstKeyNS <= 0 || s.FirstKeyNS > m.result.FirstActionNS || s.LastKeyNS < m.result.LastActionNS {
		return errors.New("manual_input_unverified")
	}
	m.result.Status = "verified_manual_fixture_challenge"
	return nil
}
