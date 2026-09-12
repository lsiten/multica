package main

import (
	"crypto/sha256"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func loadBuiltinTURNConfig() mirror.BuiltinTURNConfig {
	port := envPositiveInt("MULTICA_TURN_PORT", 3478)
	ttl := envDuration("MULTICA_TURN_CREDENTIAL_TTL", time.Hour)
	secret := strings.TrimSpace(os.Getenv("MULTICA_TURN_SECRET"))
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("JWT_SECRET"))
	}
	config := mirror.BuiltinTURNConfig{
		// Docker Compose and the Helm chart set this explicitly. Default to off
		// for bare-metal/managed API processes that do not run the bundled coturn
		// container; otherwise the advertised hostname would look configured
		// even though no relay was deployed.
		Enabled:    strings.EqualFold(strings.TrimSpace(os.Getenv("MULTICA_TURN_ENABLED")), "true"),
		Host:       turnPublicHost(),
		Port:       port,
		Secret:     secret,
		TTL:        ttl,
		Transports: []string{"udp", "tcp"},
	}
	if config.Enabled && !config.Configured() {
		slog.Warn("screen mirror built-in TURN is enabled but unusable: set MULTICA_TURN_PUBLIC_HOST to the server's public hostname or IP (localhost is ignored) so peers across restrictive networks can relay")
	}
	return config
}

func loadCloudflareTURNConfig() mirror.CloudflareTURNConfig {
	ttl := envDuration("MULTICA_CLOUDFLARE_TURN_TTL", 24*time.Hour)
	return mirror.CloudflareTURNConfig{
		KeyID:    strings.TrimSpace(os.Getenv("MULTICA_CLOUDFLARE_TURN_KEY_ID")),
		APIToken: strings.TrimSpace(os.Getenv("MULTICA_CLOUDFLARE_TURN_API_TOKEN")),
		TTL:      ttl,
		Endpoint: strings.TrimSpace(os.Getenv("MULTICA_CLOUDFLARE_TURN_ENDPOINT")),
	}
}

func turnPublicHost() string {
	for _, value := range []string{
		os.Getenv("MULTICA_TURN_PUBLIC_HOST"),
		os.Getenv("MULTICA_PUBLIC_URL"),
		appURLFromEnv(),
	} {
		host := turnHostFromValue(value)
		if host != "" {
			return host
		}
	}
	return ""
}

func turnHostFromValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		// url.Parse treats a bare turn.example.com as a path rather than a host.
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Hostname())
}

func loadMirrorNetworkSecretBox() *secretbox.Box {
	material := strings.TrimSpace(os.Getenv("MULTICA_MIRROR_NETWORK_SECRET_KEY"))
	if material == "" {
		material = strings.TrimSpace(os.Getenv("JWT_SECRET"))
	}
	if material == "" {
		return nil
	}
	sum := sha256.Sum256([]byte("mirror-network:" + material))
	box, err := secretbox.New(sum[:])
	if err != nil {
		return nil
	}
	return box
}

func newCloudflareTURNProviderFromEnv() *mirror.CloudflareTURNProvider {
	config := loadCloudflareTURNConfig()
	if !config.Configured() {
		return nil
	}
	return mirror.NewCloudflareTURNProvider(config)
}
