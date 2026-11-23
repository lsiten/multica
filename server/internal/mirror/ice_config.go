package mirror

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type ICEPlan struct {
	ICEServers      []ICEServer `json:"ice_servers"`
	TURNConfigured  bool        `json:"turn_configured"`
	TransportPolicy string      `json:"transport_policy"`
}

func (plan ICEPlan) Protocol() protocol.MirrorICEConfig {
	servers := make([]protocol.MirrorICEServer, 0, len(plan.ICEServers))
	for _, server := range plan.ICEServers {
		servers = append(servers, protocol.MirrorICEServer{
			URLs:       append([]string(nil), server.URLs...),
			Username:   server.Username,
			Credential: server.Credential,
		})
	}
	policy := plan.TransportPolicy
	if !KnownTransportPolicy(policy) {
		policy = NetworkTransportPolicyAll
	}
	return protocol.MirrorICEConfig{
		ICEServers:         servers,
		TURNConfigured:     plan.TURNConfigured,
		ICETransportPolicy: policy,
	}
}

// ICEConfigFromProtocol converts the control-plane ICE configuration into the
// daemon peer-connection configuration without sharing the protocol package
// through RuntimeMirror's constructor.
func ICEConfigFromProtocol(config protocol.MirrorICEConfig) ICEConfig {
	servers := make([]webrtc.ICEServer, 0, len(config.ICEServers))
	for _, server := range config.ICEServers {
		if len(server.URLs) == 0 {
			continue
		}
		servers = append(servers, webrtc.ICEServer{
			URLs:       append([]string(nil), server.URLs...),
			Username:   server.Username,
			Credential: server.Credential,
		})
	}
	policy := config.ICETransportPolicy
	if !KnownTransportPolicy(policy) {
		policy = NetworkTransportPolicyAll
	}
	return ICEConfig{ICEServers: servers, TransportPolicy: policy}
}
