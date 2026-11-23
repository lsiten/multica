package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/mirror"
)

const (
	mirrorICEEnvVar       = "MULTICA_MIRROR_ICE_SERVERS"
	mirrorICEPolicyEnvVar = "MULTICA_MIRROR_ICE_TRANSPORT_POLICY"
)

type mirrorICEServerWire struct {
	URLs       json.RawMessage `json:"urls"`
	Username   string          `json:"username,omitempty"`
	Credential string          `json:"credential,omitempty"`
}

// LoadMirrorICEPlanFromEnv reads the deployment STUN/TURN configuration.
// A malformed value is logged and leaves mirroring usable on local networks
// rather than preventing the whole API from starting.
func LoadMirrorICEPlanFromEnv() mirror.ICEPlan {
	raw := strings.TrimSpace(os.Getenv(mirrorICEEnvVar))
	if raw == "" {
		return mirror.ICEPlan{TransportPolicy: configuredMirrorICEPolicy()}
	}
	plan, err := ParseMirrorICEPlan(raw)
	if err != nil {
		slog.Warn("mirror ICE configuration disabled", "env", mirrorICEEnvVar, "error", err)
		return mirror.ICEPlan{}
	}
	plan.TransportPolicy = configuredMirrorICEPolicy()
	return plan
}

func configuredMirrorICEPolicy() string {
	policy := strings.TrimSpace(os.Getenv(mirrorICEPolicyEnvVar))
	if policy == "" {
		return ""
	}
	if !mirror.KnownTransportPolicy(policy) {
		slog.Warn("mirror ICE transport policy is invalid; using all", "env", mirrorICEPolicyEnvVar, "value", policy)
		return mirror.NetworkTransportPolicyAll
	}
	return policy
}

func ParseMirrorICEPlan(raw string) (mirror.ICEPlan, error) {
	var wireServers []mirrorICEServerWire
	if err := json.Unmarshal([]byte(raw), &wireServers); err != nil {
		return mirror.ICEPlan{}, fmt.Errorf("parse %s: %w", mirrorICEEnvVar, err)
	}
	plan := mirror.ICEPlan{ICEServers: make([]mirror.ICEServer, 0, len(wireServers))}
	for index, wire := range wireServers {
		urls, err := parseMirrorICEURLs(wire.URLs)
		if err != nil {
			return mirror.ICEPlan{}, fmt.Errorf("parse %s server %d: %w", mirrorICEEnvVar, index, err)
		}
		if len(urls) == 0 {
			continue
		}
		for _, url := range urls {
			if strings.HasPrefix(url, "turn:") || strings.HasPrefix(url, "turns:") {
				plan.TURNConfigured = true
			}
		}
		plan.ICEServers = append(plan.ICEServers, mirror.ICEServer{
			URLs:       urls,
			Username:   strings.TrimSpace(wire.Username),
			Credential: strings.TrimSpace(wire.Credential),
		})
	}
	return plan, nil
}

func parseMirrorICEURLs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if url := strings.TrimSpace(single); url != "" {
			return []string{url}, nil
		}
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("urls must be a string or string array: %w", err)
	}
	urls := make([]string, 0, len(values))
	for _, value := range values {
		if url := strings.TrimSpace(value); url != "" {
			urls = append(urls, url)
		}
	}
	return urls, nil
}
