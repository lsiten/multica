package application

import "time"

// InstanceStatus projects desired and observed state without treating lost contact as process exit.
func InstanceStatus(desired, process, health, runtimeState string, generation, observedGeneration int64, observedAt, lastSeenAt *time.Time, now time.Time) string {
	connected := runtimeState == "online" && lastSeenAt != nil && now.Sub(*lastSeenAt) < 3*time.Minute
	if !connected {
		return "offline"
	}
	if generation != observedGeneration {
		if desired == "stopped" {
			return "stopping"
		}
		return "starting"
	}
	if observedAt == nil || now.Sub(*observedAt) > 45*time.Second {
		return "unknown"
	}
	switch process {
	case "preparing", "starting":
		return "starting"
	case "stopping":
		return "stopping"
	case "stopped":
		return "stopped"
	case "failed":
		return "failed"
	case "running":
		if health == "healthy" || health == "none" {
			return "running"
		}
		if health == "unhealthy" {
			return "unhealthy"
		}
		return "starting"
	default:
		return "unknown"
	}
}
