package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (report terminalTaskReport) storageKey() string {
	if report.kind == applicationStepReport {
		return "application-step/" + report.runtimeID + "/" + report.taskID + "/" + report.applicationStep.Result.ClaimToken
	}
	if report.kind == applicationObservationReport {
		return "application/" + report.runtimeID + "/" + report.taskID
	}
	if report.kind != taskMessagesReport && report.kind != taskUsageReport {
		return report.taskID
	}
	raw, err := terminalReportPayload(report)
	if err != nil {
		return "invalid-report/" + report.taskID
	}
	sum := sha256.Sum256(raw)
	return report.taskID + "/" + hex.EncodeToString(sum[:])
}

func stableTaskMessages(taskID, executionID string, messages []TaskMessageData) ([]TaskMessageData, error) {
	prepared := append([]TaskMessageData(nil), messages...)
	for i := range prepared {
		if prepared[i].ID != "" {
			continue
		}
		raw, err := json.Marshal(prepared[i])
		if err != nil {
			return nil, fmt.Errorf("encode task message identity: %w", err)
		}
		prepared[i].ID = uuid.NewSHA1(uuid.NameSpaceOID, append([]byte(taskID+"\x00"+executionID+"\x00"), raw...)).String()
	}
	return prepared, nil
}

// queueTaskReport makes local durability the foreground completion boundary.
func (d *Daemon) queueTaskReport(report terminalTaskReport) error {
	if report.accountID == "" {
		report.accountID = d.accountID
	}
	if d.terminalReports == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return d.sendTerminalTaskReport(ctx, report, nil)
	}
	if err := d.terminalReports.enqueue(report); err != nil {
		return err
	}
	d.signalTerminalReportReplay()
	return nil
}
