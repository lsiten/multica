package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultCloudflareTURNEndpoint is the Cloudflare Realtime TURN API base URL.
const DefaultCloudflareTURNEndpoint = "https://rtc.live.cloudflare.com"

// CloudflareTURNConfig holds the deployment's Cloudflare TURN key. Key ID and
// API token are created once in the Cloudflare dashboard (Realtime -> TURN);
// the long-lived token never reaches browsers. The backend exchanges it for
// short-lived ICE credentials on a schedule.
type CloudflareTURNConfig struct {
	KeyID    string
	APIToken string
	TTL      time.Duration
	Endpoint string
}

// Configured reports whether a usable Cloudflare TURN key is present.
func (config CloudflareTURNConfig) Configured() bool {
	return strings.TrimSpace(config.KeyID) != "" && strings.TrimSpace(config.APIToken) != ""
}

// BuiltinNetwork is any deployment-provided ICE plan source. Both the local
// coturn config and the Cloudflare TURN provider satisfy it.
type BuiltinNetwork interface {
	Configured() bool
	Plan(now time.Time, identity string) ICEPlan
}

type builtinChain struct {
	providers []BuiltinNetwork
}

// NewBuiltinNetworkChain tries each configured provider in order and returns
// the first plan with ICE servers. Providers whose credential refresh fails
// (empty plan) are skipped so a later provider can still serve the session.
func NewBuiltinChain(providers ...BuiltinNetwork) BuiltinNetwork {
	chain := builtinChain{providers: make([]BuiltinNetwork, 0, len(providers))}
	for _, provider := range providers {
		if provider != nil {
			chain.providers = append(chain.providers, provider)
		}
	}
	return chain
}

func (chain builtinChain) Configured() bool {
	for _, provider := range chain.providers {
		if provider.Configured() {
			return true
		}
	}
	return false
}

func (chain builtinChain) Plan(now time.Time, identity string) ICEPlan {
	for _, provider := range chain.providers {
		if !provider.Configured() {
			continue
		}
		if plan := provider.Plan(now, identity); len(plan.ICEServers) > 0 {
			return plan
		}
	}
	return ICEPlan{}
}

const (
	cloudflareTURNMaxTTL = 48 * time.Hour
	cloudflareTURNMinTTL = 5 * time.Minute
	// Refresh a bit before the credentials expire so peers never start a
	// session with credentials that die mid-negotiation.
	cloudflareTURNRefreshMargin = 10 * time.Minute
	cloudflareTURNHTTPTimeout   = 10 * time.Second
)

// CloudflareTURNProvider exchanges the long-lived Cloudflare TURN key for
// short-lived ICE credentials and caches them in memory. A single backend
// process serves every viewer/daemon from the same cached credentials.
type CloudflareTURNProvider struct {
	config CloudflareTURNConfig
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	plan      ICEPlan
	expiresAt time.Time
}

// NewCloudflareTURNProvider builds a provider, clamps the requested TTL to
// Cloudflare's limits, and eagerly refreshes credentials in the background so
// the first mirror session does not wait on the token exchange.
func NewCloudflareTURNProvider(config CloudflareTURNConfig) *CloudflareTURNProvider {
	if strings.TrimSpace(config.Endpoint) == "" {
		config.Endpoint = DefaultCloudflareTURNEndpoint
	}
	if config.TTL <= 0 {
		config.TTL = 24 * time.Hour
	}
	if config.TTL > cloudflareTURNMaxTTL {
		config.TTL = cloudflareTURNMaxTTL
	}
	if config.TTL < cloudflareTURNMinTTL {
		config.TTL = cloudflareTURNMinTTL
	}
	provider := &CloudflareTURNProvider{
		config: config,
		client: &http.Client{Timeout: cloudflareTURNHTTPTimeout},
		now:    time.Now,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cloudflareTURNHTTPTimeout)
		defer cancel()
		if err := provider.refresh(ctx); err != nil {
			slog.Warn("initial Cloudflare TURN credential refresh failed; mirroring retries on next session", "error", err)
		}
	}()
	return provider
}

func (provider *CloudflareTURNProvider) Configured() bool {
	return provider.config.Configured()
}

// TTL exposes the clamped credential lifetime for operator-facing status.
func (provider *CloudflareTURNProvider) TTL() time.Duration {
	return provider.config.TTL
}

// Healthy reports whether cached credentials are currently usable.
func (provider *CloudflareTURNProvider) Healthy() bool {
	if !provider.Configured() {
		return false
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.plan.ICEServers) > 0 && provider.now().Before(provider.expiresAt)
}

// Plan returns cached ICE credentials, refreshing them shortly before expiry.
// On refresh failure it keeps serving unexpired credentials; once they expire
// it returns an empty plan so the builtin chain can try another provider.
func (provider *CloudflareTURNProvider) Plan(now time.Time, _ string) ICEPlan {
	provider.mu.Lock()
	stale := len(provider.plan.ICEServers) == 0 || !now.Add(cloudflareTURNRefreshMargin).Before(provider.expiresAt)
	plan, expiresAt := provider.plan, provider.expiresAt
	provider.mu.Unlock()

	if !stale {
		return plan
	}

	ctx, cancel := context.WithTimeout(context.Background(), cloudflareTURNHTTPTimeout)
	defer cancel()
	if err := provider.refresh(ctx); err != nil {
		slog.Warn("Cloudflare TURN credential refresh failed", "error", err)
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if len(provider.plan.ICEServers) > 0 && provider.now().Before(provider.expiresAt) {
			return provider.plan
		}
		return ICEPlan{}
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	_ = expiresAt
	return provider.plan
}

func (provider *CloudflareTURNProvider) refresh(ctx context.Context) error {
	ttlSeconds := int64(provider.config.TTL / time.Second)
	requestBody, _ := json.Marshal(map[string]int64{"ttl": ttlSeconds})
	endpoint := strings.TrimRight(provider.config.Endpoint, "/") +
		fmt.Sprintf("/v1/turn/keys/%s/credentials/generate-ice-servers", provider.config.KeyID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+provider.config.APIToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := provider.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Cloudflare TURN API returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	plan, err := parseCloudflareICEResponse(body)
	if err != nil {
		return err
	}

	provider.mu.Lock()
	provider.plan = plan
	provider.expiresAt = provider.now().Add(provider.config.TTL)
	provider.mu.Unlock()
	return nil
}

// parseCloudflareICEResponse accepts both the current documented array shape
// ({"iceServers":[{...},{...}]}) and the older single-object shape
// ({"iceServers":{"urls":[...],"username":...,"credential":...}}) returned by
// the endpoint's first revision.
func parseCloudflareICEResponse(body []byte) (ICEPlan, error) {
	var envelope struct {
		ICEServers json.RawMessage `json:"iceServers"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ICEPlan{}, fmt.Errorf("decode Cloudflare TURN response: %w", err)
	}
	var entries []cloudflareICEEntry
	if len(envelope.ICEServers) > 0 && envelope.ICEServers[0] == '[' {
		if err := json.Unmarshal(envelope.ICEServers, &entries); err != nil {
			return ICEPlan{}, fmt.Errorf("decode Cloudflare TURN iceServers array: %w", err)
		}
	} else {
		var single cloudflareICEEntry
		if err := json.Unmarshal(envelope.ICEServers, &single); err != nil {
			return ICEPlan{}, fmt.Errorf("decode Cloudflare TURN iceServers object: %w", err)
		}
		entries = []cloudflareICEEntry{single}
	}

	plan := ICEPlan{ICEServers: make([]ICEServer, 0, len(entries))}
	for _, entry := range entries {
		urls := make([]string, 0, len(entry.URLs))
		for _, raw := range entry.URLs {
			// Browsers hang on the alternate port 53 candidates; Cloudflare
			// itself recommends filtering them when trickle ICE is absent.
			if isPort53ICEURL(raw) {
				continue
			}
			urls = append(urls, raw)
		}
		if len(urls) == 0 {
			continue
		}
		plan.ICEServers = append(plan.ICEServers, ICEServer{
			URLs:       urls,
			Username:   entry.Username,
			Credential: entry.Credential,
		})
		if HasTURNURL(urls) {
			plan.TURNConfigured = true
		}
	}
	if len(plan.ICEServers) == 0 {
		return ICEPlan{}, fmt.Errorf("Cloudflare TURN response contained no usable ICE servers")
	}
	return plan, nil
}

type cloudflareICEEntry struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
}

func isPort53ICEURL(raw string) bool {
	value := raw
	if index := strings.IndexAny(value, "?/#"); index >= 0 {
		value = value[:index]
	}
	// authority follows the "scheme:" prefix, optionally with "//".
	if index := strings.Index(value, ":"); index >= 0 {
		value = strings.TrimPrefix(value[index+1:], "//")
	}
	// Strip IPv6 brackets, if any, otherwise compare against host:port.
	value = strings.TrimSuffix(value, "]")
	if strings.HasPrefix(value, "[") {
		return false
	}
	return strings.HasSuffix(value, ":53")
}
