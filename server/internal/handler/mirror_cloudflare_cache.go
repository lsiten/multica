package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
)

// cloudflareProviderCache keeps one live Cloudflare TURN provider per
// workspace credential set so credentials are shared and only refreshed once
// per TTL window, instead of a provider (and its background refresh goroutine)
// being created on every mirror session request.
type cloudflareProviderCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*mirror.CloudflareTURNProvider
}

func newCloudflareProviderCache(ttl time.Duration) *cloudflareProviderCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &cloudflareProviderCache{ttl: ttl, entries: map[string]*mirror.CloudflareTURNProvider{}}
}

func (cache *cloudflareProviderCache) provider(keyID, apiToken string) *mirror.CloudflareTURNProvider {
	if keyID == "" || apiToken == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(keyID + "\x00" + apiToken))
	key := hex.EncodeToString(sum[:])

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if provider, ok := cache.entries[key]; ok {
		return provider
	}
	provider := mirror.NewCloudflareTURNProvider(mirror.CloudflareTURNConfig{
		KeyID:    keyID,
		APIToken: apiToken,
		TTL:      cache.ttl,
	})
	cache.entries[key] = provider
	return provider
}
