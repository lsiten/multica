package runtimeproc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
)

func (s *Service) acknowledgementTargetsLocked(raw json.RawMessage) ([]string, *Error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		ids := make([]string, 0, len(s.record.Operations))
		for id, receipt := range s.record.Operations {
			if receipt.State != "completed" {
				return nil, &Error{Code: "uncertain_operation", Message: "pending receipts cannot be retired"}
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	var payload AcknowledgePayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || len(payload.RequestIDs) == 0 {
		return nil, &Error{Code: "malformed", Message: "nonempty request_ids required"}
	}
	seen := make(map[string]bool, len(payload.RequestIDs))
	for _, id := range payload.RequestIDs {
		if id == "" || seen[id] {
			return nil, &Error{Code: "malformed", Message: "request_ids must be nonempty and unique"}
		}
		seen[id] = true
		receipt, ok := s.record.Operations[id]
		if !ok {
			return nil, &Error{Code: "not_found", Message: "selected receipt does not exist"}
		}
		if receipt.State != "completed" {
			return nil, &Error{Code: "uncertain_operation", Message: "selected receipt is still pending"}
		}
	}
	return payload.RequestIDs, nil
}

func (s *Service) receiptUsageLocked() ReceiptUsage {
	usage := ReceiptUsage{Total: len(s.record.Operations), Limit: maxOperations}
	for _, receipt := range s.record.Operations {
		if receipt.State == "completed" {
			usage.Completed++
		} else {
			usage.Pending++
		}
	}
	return usage
}

func (s *Service) receiptCapacityMessageLocked() string {
	usage := s.receiptUsageLocked()
	ids := make([]string, 0, len(s.record.Operations))
	for id, receipt := range s.record.Operations {
		if receipt.State == "pending" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	message := fmt.Sprintf("receipt quota reached: total=%d pending=%d completed=%d limit=%d; acknowledge consumed completed receipts", usage.Total, usage.Pending, usage.Completed, usage.Limit)
	if len(ids) > 0 {
		receipt := s.record.Operations[ids[0]]
		message += fmt.Sprintf("; unresolved request=%s operation=%s", ids[0], receipt.Operation)
	}
	return message
}

// completeLocked stages a new journal, retaining the admitted intent on failure.
func (s *Service) completeLocked(req Request, receipt *Receipt, retired []string) error {
	next := s.record
	next.Operations = maps.Clone(s.record.Operations)
	next.Fence.Revision++
	if receipt.Error == nil {
		switch req.Operation {
		case "drain":
			next.State = "draining"
		case "resume":
			next.State = "ready"
		case "stop":
			next.State = "stopped"
		case "acknowledge":
			next.ReplayEpoch++
			for _, id := range retired {
				delete(next.Operations, id)
			}
			if previous, ok := next.Operations[next.AcknowledgementID]; ok && previous.State == "completed" {
				delete(next.Operations, next.AcknowledgementID)
			}
			next.AcknowledgementID = req.RequestID
		}
	}
	receipt.Fence = next.Fence
	next.Operations[req.RequestID] = *receipt
	if err := writeRecord(s.path, next); err != nil {
		s.record.State = "suspect"
		return err
	}
	s.record = next
	return nil
}
