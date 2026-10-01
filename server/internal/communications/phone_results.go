package communications

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (c *TwilioClient) CancelCall(ctx context.Context, sid string) (Call, error) {
	call, err := c.GetCall(ctx, sid)
	if err != nil {
		return Call{}, err
	}
	status := "canceled"
	switch call.Status {
	case "queued", "ringing":
	case "in-progress":
		status = "completed"
	case "completed", "canceled", "failed", "busy", "no-answer":
		return call, nil
	default:
		return Call{}, fmt.Errorf("phone provider returned unknown call status")
	}
	if err := c.do(ctx, http.MethodPost, c.path("Calls", sid+".json"), url.Values{"Status": {status}}, &call); err != nil {
		return Call{}, err
	}
	if call.SID != sid || call.Status == "" {
		return Call{}, ErrCallNotFound
	}
	return call, nil
}

// Provider transcript reads are restricted to recordings belonging to this call.
// Recording transcription is a legacy Twilio API; it must already be enabled
// on the provider account. Empty output is not proof that transcription finished.
func (c *TwilioClient) ListTranscriptions(ctx context.Context, sid string) ([]Transcript, error) {
	if sid == "" || strings.ContainsAny(sid, "/\\") {
		return nil, ErrCallNotFound
	}
	var recordings struct {
		Recordings []struct {
			SID     string `json:"sid"`
			CallSID string `json:"call_sid"`
		} `json:"recordings"`
		Next string `json:"next_page_uri"`
	}
	if err := c.do(ctx, http.MethodGet, c.path("Calls", sid, "Recordings.json")+"?PageSize=20", nil, &recordings); err != nil {
		return nil, err
	}
	if recordings.Next != "" || len(recordings.Recordings) > 20 {
		return nil, fmt.Errorf("recording result exceeds supported page; narrow the call recording history")
	}
	out := make([]Transcript, 0)
	for _, recording := range recordings.Recordings {
		if recording.SID == "" || recording.CallSID != sid || strings.ContainsAny(recording.SID, "/\\") {
			return nil, ErrInvalidRequest
		}
		var page struct {
			Transcriptions []Transcript `json:"transcriptions"`
			Next           string       `json:"next_page_uri"`
		}
		if err := c.do(ctx, http.MethodGet, c.path("Recordings", recording.SID, "Transcriptions.json")+"?PageSize=20", nil, &page); err != nil {
			return nil, err
		}
		if page.Next != "" || len(page.Transcriptions) > 20 {
			return nil, fmt.Errorf("transcription result exceeds supported page")
		}
		for _, transcript := range page.Transcriptions {
			if transcript.RecordingSID != recording.SID {
				return nil, ErrInvalidRequest
			}
			transcript.CallSID = sid
			out = append(out, transcript)
		}
	}
	return out, nil
}
