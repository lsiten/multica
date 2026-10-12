package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"time"
)

func (d *Daemon) queueApplicationObservation(runtimeID string, observation protocol.ApplicationObservation) error {
	key := runtimeID + "/" + observation.InstanceID
	desired, intentID := "", ""
	intent, err := d.readLocalIntent(observation.InstanceID)
	if err != nil {
		return err
	}
	if intent != nil && intent.Generation == observation.Generation && intent.Result != nil && !intent.Synced {
		desired, intentID = intent.DesiredState, intent.Input.CommandID
	}
	raw, err := json.Marshal(struct {
		Observation            protocol.ApplicationObservation
		DesiredState, IntentID string
	}{observation, desired, intentID})
	if err != nil {
		return err
	}
	if prior, ok := d.applicationObservationTimes.Load(key); ok {
		cached := prior.(queuedObservationTime)
		if cached.payload == string(raw) && d.reportClock().Sub(cached.at) < 20*time.Second {
			return nil
		}
	}
	d.mu.Lock()
	if d.terminalReports == nil {
		d.terminalReports = newTerminalReportStore(d.cfg)
		d.terminalReports.bindAccount(d.accountID)
	}
	d.mu.Unlock()
	if d.terminalReports == nil {
		return errors.New("application observation storage unavailable")
	}
	if err := d.queueTaskReport(terminalTaskReport{kind: applicationObservationReport, taskID: observation.InstanceID, runtimeID: runtimeID, applicationObservation: &observation, applicationDesiredState: desired, applicationIntentID: intentID}); err != nil {
		return err
	}
	d.applicationObservationTimes.Store(key, queuedObservationTime{payload: string(raw), at: d.reportClock()})
	return nil
}

func (d *Daemon) ackLocalApplicationIntent(ctx context.Context, report terminalTaskReport) error {
	if report.applicationIntentID == "" || report.applicationDesiredState == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	release, err := d.lockLocalApplicationIntent(ctx, report.taskID)
	if err != nil {
		return err
	}
	defer release()
	intent, err := d.readLocalIntent(report.taskID)
	if err != nil {
		return err
	}
	if intent == nil || intent.Synced || intent.Result == nil || intent.Input.RuntimeID != report.runtimeID || intent.Input.CommandID != report.applicationIntentID || intent.Generation != report.applicationObservation.Generation || intent.DesiredState != report.applicationDesiredState {
		return nil
	}
	intent.Synced = true
	return d.writeLocalIntent(*intent)
}

type queuedObservationTime struct {
	payload string
	at      time.Time
}
