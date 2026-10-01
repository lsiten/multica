package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPPhoneClient is a provider-neutral adapter for domestic gateways. The
// gateway contract is deliberately small and maps to the same CallProvider
// used by Twilio; provider-specific signing stays in the gateway, never in an
// agent task. It supports POST /calls, GET /calls/{sid}, POST
// /calls/{sid}/cancel and GET /calls/{sid}/transcriptions.
type HTTPPhoneClient struct {
	cfg   Config
	http  *http.Client
	store IdempotencyStore
}

func NewHTTPPhoneClient(cfg Config, client *http.Client, store IdempotencyStore) (*HTTPPhoneClient, error) {
	base := strings.TrimRight(cfg.HTTPBaseURL, "/")
	u, err := url.Parse(base)
	providedClient := client != nil
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(providedClient && isLocalProviderURL(u))) ||
		strings.TrimSpace(cfg.HTTPToken) == "" || !e164Pattern.MatchString(cfg.FromNumber) {
		return nil, ErrNotConfigured
	}
	if !validHTTPSCallback(cfg.StatusCallbackURL) || !validHTTPSCallback(cfg.RecordingStatusCallbackURL) {
		return nil, ErrInvalidRequest
	}
	cfg.HTTPBaseURL = base
	cfg.HTTPToken = strings.TrimSpace(cfg.HTTPToken)
	if client == nil {
		client = &http.Client{}
	}
	owned := *client
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if store == nil {
		store = NewMemoryIdempotencyStore()
	}
	return &HTTPPhoneClient{cfg: cfg, http: &owned, store: store}, nil
}

func (c *HTTPPhoneClient) StartCall(ctx context.Context, req CallRequest) (Call, error) {
	if err := validateCallRequest(req); err != nil {
		return Call{}, err
	}
	if !validHTTPSCallback(req.StatusCallback) {
		return Call{}, ErrInvalidRequest
	}
	var reserved bool
	var err error
	if store, ok := c.store.(FingerprintedIdempotencyStore); ok {
		reserved, err = store.ReserveFingerprint(req.IdempotencyKey, phoneRequestFingerprint(req, c.cfg))
	} else {
		reserved, err = c.store.Reserve(req.IdempotencyKey)
	}
	if err != nil {
		return Call{}, err
	}
	if !reserved {
		if existing, found, pending := c.store.Get(req.IdempotencyKey); found && !pending {
			return existing, nil
		}
		return Call{}, ErrAmbiguousOperation
	}
	var out Call
	statusCallback := req.StatusCallback
	if statusCallback == "" {
		statusCallback = c.cfg.StatusCallbackURL
	}
	err = c.do(ctx, http.MethodPost, "/calls", map[string]any{"to": req.To, "from": c.cfg.FromNumber, "message": req.Message, "twiml": req.Twiml, "record": req.Record, "status_callback": statusCallback, "recording_status_callback": c.cfg.RecordingStatusCallbackURL, "idempotency_key": req.IdempotencyKey}, &out)
	if err != nil {
		// A transport error or a malformed response is ambiguous: the gateway
		// may already have accepted the call, so retain the reservation and force
		// the caller to query status before retrying. Only a deterministic client
		// rejection can safely release the idempotency key.
		var providerErr *ProviderError
		if errors.Is(err, ErrCallNotFound) || (errors.As(err, &providerErr) && providerErr.StatusCode >= 400 && providerErr.StatusCode < 500) {
			if storeErr := c.store.Forget(req.IdempotencyKey); storeErr != nil {
				return Call{}, errors.Join(err, storeErr)
			}
		}
		return Call{}, err
	}
	if out.SID == "" || out.Status == "" {
		return Call{}, errors.New("phone provider returned no call SID")
	}
	if (out.To != "" && out.To != req.To) || (out.From != "" && out.From != c.cfg.FromNumber) {
		return Call{}, errors.New("phone provider returned invalid call identity")
	}
	if err := c.store.Complete(req.IdempotencyKey, out); err != nil {
		return Call{}, err
	}
	return out, nil
}
func (c *HTTPPhoneClient) GetCall(ctx context.Context, sid string) (Call, error) {
	if !validCallSID(sid) {
		return Call{}, ErrCallNotFound
	}
	var out Call
	if err := c.do(ctx, http.MethodGet, "/calls/"+url.PathEscape(sid), nil, &out); err != nil {
		return Call{}, err
	}
	if out.SID != sid || out.Status == "" {
		return Call{}, ErrCallNotFound
	}
	return out, nil
}
func (c *HTTPPhoneClient) CancelCall(ctx context.Context, sid string) (Call, error) {
	if !validCallSID(sid) {
		return Call{}, ErrCallNotFound
	}
	var out Call
	if err := c.do(ctx, http.MethodPost, "/calls/"+url.PathEscape(sid)+"/cancel", nil, &out); err != nil {
		return Call{}, err
	}
	if out.SID != sid || out.Status == "" {
		return Call{}, ErrCallNotFound
	}
	return out, nil
}
func (c *HTTPPhoneClient) ListTranscriptions(ctx context.Context, sid string) ([]Transcript, error) {
	if !validCallSID(sid) {
		return nil, ErrCallNotFound
	}
	var out struct {
		Transcriptions []Transcript `json:"transcriptions"`
	}
	if err := c.do(ctx, http.MethodGet, "/calls/"+url.PathEscape(sid)+"/transcriptions", nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Transcriptions {
		if out.Transcriptions[i].CallSID != "" && out.Transcriptions[i].CallSID != sid {
			return nil, ErrCallNotFound
		}
		out.Transcriptions[i].CallSID = sid
	}
	return out.Transcriptions, nil
}

func validCallSID(sid string) bool {
	return sid != "" && !strings.ContainsAny(sid, "/\\")
}

func validHTTPSCallback(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func (c *HTTPPhoneClient) do(ctx context.Context, method, path string, payload map[string]any, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var body *strings.Reader
	if payload == nil {
		body = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(callCtx, method, strings.TrimRight(c.cfg.HTTPBaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.HTTPToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrCallNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ProviderError{StatusCode: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode phone provider response: %w", err)
	}
	return nil
}
