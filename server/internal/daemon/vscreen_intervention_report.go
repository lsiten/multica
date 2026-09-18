package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) initVscreenReporter(ctx context.Context) (*vscreenReporter, error) {
	if d.cfg.NativeVscreenPreferencesPath == "" {
		return nil, errors.New("virtual screen durable report path unavailable")
	}
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var account struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(query, "/api/me", &account); err != nil || account.ID == "" {
		return nil, errors.New("virtual screen report account unavailable")
	}
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	if d.vscreenReporter != nil {
		if d.vscreenReporter.config.AccountID == account.ID {
			return d.vscreenReporter, nil
		}
		if err := d.vscreenReporter.CancelScope("", ""); err != nil {
			return nil, err
		}
		if err := d.vscreenReporter.Close(); err != nil {
			return nil, err
		}
		d.vscreenReporter = nil
	}
	digest := sha256.Sum256([]byte(d.cfg.ServerBaseURL + "\x00" + account.ID))
	filename := filepath.Join(d.cfg.NativeVscreenPreferencesPath+".interventions", hex.EncodeToString(digest[:])+".json")
	reporter, err := newVscreenReporter(vscreenReportConfig{Path: filename, BackendIdentity: d.cfg.ServerBaseURL, AccountID: account.ID, OnResult: func(report protocol.VscreenIntervention, reason string) {
		if reason != "" {
			d.logger.Warn("virtual screen intervention report refused", "intervention_id", report.InterventionID, "reason", reason)
		}
	}})
	if err != nil {
		return nil, err
	}
	d.vscreenReporter = reporter
	return reporter, nil
}
func (d *Daemon) enqueueVscreenIntervention(ctx context.Context, report protocol.VscreenIntervention) error {
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	d.vscreenMu.Unlock()
	if reporter == nil {
		var err error
		reporter, err = d.initVscreenReporter(ctx)
		if err != nil {
			return err
		}
	}
	report.HumanSummary = ""
	return reporter.Queue(ctx, report)
}
func (d *Daemon) closeVscreenReporter() {
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	d.vscreenReporter = nil
	d.vscreenMu.Unlock()
	if reporter != nil {
		if err := reporter.Close(); err != nil {
			d.logger.Warn("virtual screen report close failed")
		}
	}
}
