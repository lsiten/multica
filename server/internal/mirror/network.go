package mirror

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	NetworkModeBuiltin  = "builtin"
	NetworkModeCustom   = "custom"
	NetworkModeDisabled = "disabled"
)

const (
	NetworkTransportPolicyAll   = "all"
	NetworkTransportPolicyRelay = "relay"
)

const (
	NetworkSourceEnv                = "env"
	NetworkSourceBuiltin            = "builtin"
	NetworkSourceCustom             = "custom"
	NetworkSourceDisabled           = "disabled"
	NetworkSourceBuiltinUnavailable = "builtin_unavailable"
)

type NetworkSettings struct {
	Mode            string                `json:"mode"`
	TransportPolicy string                `json:"transport_policy"`
	Servers         []StoredICEServer     `json:"servers"`
	Cloudflare      *StoredCloudflareTURN `json:"cloudflare,omitempty"`
	// ViewerNotificationsEnabled controls inbox items for read-only mirror
	// viewing start/stop. A pointer lets absent/legacy settings default to
	// enabled while an explicit false stays distinguishable.
	ViewerNotificationsEnabled *bool `json:"viewer_notifications_enabled,omitempty"`
}

// ViewerNotifications reports whether the workspace wants inbox items when a
// mirror session starts/stops. Absent/legacy settings default to enabled.
func (s NetworkSettings) ViewerNotifications() bool {
	return s.ViewerNotificationsEnabled == nil || *s.ViewerNotificationsEnabled
}

// StoredCloudflareTURN holds a workspace-managed Cloudflare Realtime TURN key.
// The long-lived API token is encrypted at rest with the deployment secret
// box; only short-lived ICE credentials ever leave the backend.
type StoredCloudflareTURN struct {
	KeyID             string `json:"key_id"`
	APITokenEncrypted string `json:"api_token_enc,omitempty"`
}

type StoredICEServer struct {
	URLs                []string `json:"urls"`
	Username            string   `json:"username,omitempty"`
	CredentialEncrypted string   `json:"credential_enc,omitempty"`
}

type BuiltinTURNConfig struct {
	Enabled    bool
	Host       string
	Port       int
	TLSPort    int
	Secret     string
	TTL        time.Duration
	Transports []string
}

type networkSettingsEnvelope struct {
	MirrorNetwork *NetworkSettings `json:"mirror_network"`
}

func DefaultNetworkSettings() NetworkSettings {
	return NetworkSettings{
		Mode:            NetworkModeBuiltin,
		TransportPolicy: NetworkTransportPolicyAll,
		Servers:         []StoredICEServer{},
	}
}

func ParseNetworkSettings(raw []byte) (NetworkSettings, error) {
	settings := DefaultNetworkSettings()
	if len(raw) == 0 {
		return settings, nil
	}
	var envelope networkSettingsEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return settings, fmt.Errorf("parse workspace mirror network settings: %w", err)
	}
	if envelope.MirrorNetwork == nil {
		return settings, nil
	}
	settings.Mode = envelope.MirrorNetwork.Mode
	settings.TransportPolicy = envelope.MirrorNetwork.TransportPolicy
	if settings.TransportPolicy == "" {
		settings.TransportPolicy = NetworkTransportPolicyAll
	}
	settings.Servers = envelope.MirrorNetwork.Servers
	settings.Cloudflare = envelope.MirrorNetwork.Cloudflare
	settings.ViewerNotificationsEnabled = envelope.MirrorNetwork.ViewerNotificationsEnabled
	if !KnownNetworkMode(settings.Mode) {
		return DefaultNetworkSettings(), fmt.Errorf("unknown mirror network mode %q", settings.Mode)
	}
	return settings, nil
}

func MarshalNetworkSettings(existing []byte, next NetworkSettings) ([]byte, error) {
	var document map[string]json.RawMessage
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &document); err != nil {
			return nil, fmt.Errorf("parse workspace settings: %w", err)
		}
	}
	if document == nil {
		document = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	document["mirror_network"] = raw
	return json.Marshal(document)
}

func KnownNetworkMode(mode string) bool {
	switch mode {
	case NetworkModeBuiltin, NetworkModeCustom, NetworkModeDisabled:
		return true
	default:
		return false
	}
}

func KnownTransportPolicy(policy string) bool {
	switch policy {
	case NetworkTransportPolicyAll, NetworkTransportPolicyRelay:
		return true
	default:
		return false
	}
}

func NormalizeICEURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	schemeEnd := strings.Index(value, ":")
	if schemeEnd <= 0 {
		return "", fmt.Errorf("invalid ICE URL %q", raw)
	}
	scheme := strings.ToLower(value[:schemeEnd])
	switch scheme {
	case "stun", "turn", "turns":
	default:
		return "", fmt.Errorf("ICE URL %q must use stun, turn, or turns", raw)
	}
	authority := value[schemeEnd+1:]
	// Strip a "//" authority prefix (allowed but redundant for stun/turn)
	// and the transport query so the host:port can be validated through a
	// http(s) URL. url.Parse treats stun:/turn: as opaque identifiers and
	// leaves Host empty, so it cannot validate these schemes directly.
	authority = strings.TrimPrefix(authority, "//")
	hostPort := authority
	if index := strings.IndexAny(hostPort, "?/#"); index >= 0 {
		hostPort = hostPort[:index]
	}
	if hostPort == "" {
		return "", fmt.Errorf("invalid ICE URL %q", raw)
	}
	// Parse the authority as an https URL purely for host/port validation.
	parsed, err := url.Parse("https://" + hostPort)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid ICE URL %q", raw)
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.ContainsAny(hostname, " /") {
		return "", fmt.Errorf("invalid ICE URL host %q", hostname)
	}
	return value, nil
}

func HasTURNURL(values []string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, "turn:") || strings.HasPrefix(value, "turns:") {
			return true
		}
	}
	return false
}

func (config BuiltinTURNConfig) Configured() bool {
	host := strings.TrimSpace(config.Host)
	if !config.Enabled || host == "" || config.Secret == "" || config.Port <= 0 || config.TTL <= 0 {
		return false
	}
	if !config.hasUsableTransport() {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsLoopback() {
		return false
	}
	return true
}

func (config BuiltinTURNConfig) hasUsableTransport() bool {
	transports := config.Transports
	if len(transports) == 0 {
		return true
	}
	for _, rawTransport := range transports {
		switch strings.ToLower(strings.TrimSpace(rawTransport)) {
		case "udp", "tcp":
			return true
		case "tls", "turns":
			if config.TLSPort > 0 {
				return true
			}
		}
	}
	return false
}

func (config BuiltinTURNConfig) Plan(now time.Time, identity string) ICEPlan {
	if !config.Configured() {
		return ICEPlan{}
	}
	expires := now.Add(config.TTL).Unix()
	username := strconv.FormatInt(expires, 10) + ":" + sanitizeTURNIdentity(identity)
	mac := hmac.New(sha1.New, []byte(config.Secret))
	_, _ = mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	host := strings.TrimSpace(config.Host)
	urls := []string{fmt.Sprintf("stun:%s:%d", host, config.Port)}
	transports := config.Transports
	if len(transports) == 0 {
		transports = []string{"udp", "tcp"}
	}
	for _, rawTransport := range transports {
		transport := strings.ToLower(strings.TrimSpace(rawTransport))
		switch transport {
		case "udp", "tcp":
			urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=%s", host, config.Port, transport))
		case "tls", "turns":
			if config.TLSPort > 0 {
				urls = append(urls, fmt.Sprintf("turns:%s:%d?transport=tcp", host, config.TLSPort))
			}
		}
	}
	return ICEPlan{
		ICEServers:     []ICEServer{{URLs: urls, Username: username, Credential: credential}},
		TURNConfigured: HasTURNURL(urls),
	}
}

func ResolveNetworkPlan(settings NetworkSettings, deployment ICEPlan, builtin BuiltinNetwork, custom []ICEServer, now time.Time, identity string) (ICEPlan, string) {
	if len(deployment.ICEServers) > 0 {
		if !KnownTransportPolicy(deployment.TransportPolicy) {
			deployment.TransportPolicy = NetworkTransportPolicyAll
		}
		return deployment, NetworkSourceEnv
	}
	transportPolicy := normalizeTransportPolicy(settings.TransportPolicy)
	if KnownTransportPolicy(deployment.TransportPolicy) {
		transportPolicy = deployment.TransportPolicy
	}
	if settings.Mode == NetworkModeCustom && len(custom) > 0 {
		plan := ICEPlan{ICEServers: custom}
		for _, server := range custom {
			if HasTURNURL(server.URLs) {
				plan.TURNConfigured = true
			}
		}
		plan.TransportPolicy = transportPolicy
		return plan, NetworkSourceCustom
	}
	if settings.Mode == NetworkModeDisabled {
		return ICEPlan{
			ICEServers:      []ICEServer{},
			TransportPolicy: transportPolicy,
		}, NetworkSourceDisabled
	}
	if builtin.Configured() {
		plan := builtin.Plan(now, identity)
		plan.TransportPolicy = transportPolicy
		return plan, NetworkSourceBuiltin
	}
	return ICEPlan{
		ICEServers:      []ICEServer{},
		TransportPolicy: transportPolicy,
	}, NetworkSourceBuiltinUnavailable
}

func normalizeTransportPolicy(policy string) string {
	if KnownTransportPolicy(policy) {
		return policy
	}
	return NetworkTransportPolicyAll
}

func sanitizeTURNIdentity(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
		if builder.Len() >= 64 {
			break
		}
	}
	if builder.Len() == 0 {
		return "viewer"
	}
	return builder.String()
}
