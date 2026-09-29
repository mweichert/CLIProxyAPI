package cliproxy

import "time"

// defaultShutdownDrainTimeout bounds how long a stopping service lets in-flight
// requests (typically long-lived model streams) finish before their connections
// are force-closed. A supervisor's stop timeout (for example systemd
// TimeoutStopSec) must exceed it, or the supervisor kills the process mid-drain.
const defaultShutdownDrainTimeout = 90 * time.Second

// shutdownDrainTimeout returns the drain window used when the service stops.
func (s *Service) shutdownDrainTimeout() time.Duration {
	if s != nil && s.drainTimeout > 0 {
		return s.drainTimeout
	}
	return defaultShutdownDrainTimeout
}
