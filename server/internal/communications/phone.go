// Package communications contains host-native external communication clients.
// Secrets are read by the daemon process and are never returned in errors.
package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotConfigured      = errors.New("phone provider is not configured")
	ErrInvalidPhone       = errors.New("phone number must use E.164 format")
	ErrInvalidRequest     = errors.New("invalid phone request")
	ErrAmbiguousOperation = errors.New("phone operation result is ambiguous; query status before retrying")
	ErrCallNotFound       = errors.New("phone call not found")
	e164Pattern           = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

type TwilioClient struct {
	cfg   Config
	http  *http.Client
	store IdempotencyStore
}

func NewTwilioClient(cfg Config, httpClient *http.Client, store IdempotencyStore) (*TwilioClient, error) {
	if cfg.AccountSID == "" || cfg.AuthToken == "" || !e164Pattern.MatchString(cfg.FromNumber) {
		return nil, ErrNotConfigured
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid phone provider base URL")
	}
	if parsed.Scheme != "https" && !(httpClient != nil && isLocalProviderURL(parsed)) {
		return nil, fmt.Errorf("phone provider base URL must use HTTPS")
	}
	if cfg.StatusCallbackURL != "" {
		callback, err := url.Parse(cfg.StatusCallbackURL)
		if err != nil || callback.Scheme != "https" || callback.Host == "" || callback.User != nil {
			return nil, ErrInvalidRequest
		}
	}
	if cfg.RecordingStatusCallbackURL != "" {
		callback, err := url.Parse(cfg.RecordingStatusCallbackURL)
		if err != nil || callback.Scheme != "https" || callback.Host == "" || callback.User != nil {
			return nil, ErrInvalidRequest
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	ownedClient := *httpClient
	ownedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	httpClient = &ownedClient
	if store == nil {
		store = NewMemoryIdempotencyStore()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &TwilioClient{cfg: cfg, http: httpClient, store: store}, nil
}

func isLocalProviderURL(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	return parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

func validateCallRequest(req CallRequest) error {
	if !e164Pattern.MatchString(req.To) {
		return ErrInvalidPhone
	}
	if (req.Twiml == "" && req.Message == "") || len(req.Twiml) > 32<<10 || len(req.Message) > 4000 || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 256 {
		return ErrInvalidRequest
	}
	if req.StatusCallback != "" {
		u, err := url.Parse(req.StatusCallback)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return ErrInvalidRequest
		}
	}
	return nil
}

func (c *TwilioClient) StartCall(ctx context.Context, req CallRequest) (Call, error) {
	if err := validateCallRequest(req); err != nil {
		return Call{}, err
	}
	if req.Twiml == "" {
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
	form := url.Values{"To": {req.To}, "From": {c.cfg.FromNumber}, "Twiml": {req.Twiml}, "TimeLimit": {"120"}, "Timeout": {"30"}}
	if req.Record {
		form.Set("Record", "true")
		if c.cfg.RecordingStatusCallbackURL != "" {
			form.Set("RecordingStatusCallback", c.cfg.RecordingStatusCallbackURL)
		}
	}
	callback := req.StatusCallback
	if callback == "" {
		callback = c.cfg.StatusCallbackURL
	}
	if callback != "" {
		form.Set("StatusCallback", callback)
	}
	var call Call
	if err := c.do(ctx, http.MethodPost, c.path("Calls.json"), form, &call); err != nil {
		var providerErr *ProviderError
		if errors.Is(err, ErrCallNotFound) || (errors.As(err, &providerErr) && providerErr.StatusCode >= 400 && providerErr.StatusCode < 500) {
			if storeErr := c.store.Forget(req.IdempotencyKey); storeErr != nil {
				return Call{}, errors.Join(err, storeErr)
			}
		}
		return Call{}, err
	}
	if call.SID == "" || call.Status == "" {
		return Call{}, fmt.Errorf("phone provider returned no call SID")
	}
	if err := c.store.Complete(req.IdempotencyKey, call); err != nil {
		return Call{}, err
	}
	return call, nil
}

func (c *TwilioClient) GetCall(ctx context.Context, sid string) (Call, error) {
	if sid == "" || strings.ContainsAny(sid, "/\\") {
		return Call{}, ErrCallNotFound
	}
	var call Call
	if err := c.do(ctx, http.MethodGet, c.path("Calls", sid+".json"), nil, &call); err != nil {
		return Call{}, err
	}
	if call.SID != sid || call.Status == "" {
		return Call{}, ErrCallNotFound
	}
	return call, nil
}

func (c *TwilioClient) path(parts ...string) string {
	segments := []string{"2010-04-01", "Accounts", url.PathEscape(c.cfg.AccountSID)}
	for _, part := range parts {
		segments = append(segments, url.PathEscape(part))
	}
	return "/" + strings.Join(segments, "/")
}

func (c *TwilioClient) do(ctx context.Context, method, endpoint string, form url.Values, output any) error {
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(callCtx, method, strings.TrimRight(c.cfg.BaseURL, "/")+endpoint, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		if resp.StatusCode == http.StatusNotFound {
			return ErrCallNotFound
		}
		return &ProviderError{StatusCode: resp.StatusCode}
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode phone provider response: %w", err)
	}
	return nil
}
