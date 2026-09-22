package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dhcp"
)

// Explicit bounded job; routine status never calls this or opens packet sockets.
func dhcpDiagnostic(service *Service, storeSettings func() (dhcp.Settings, uint64)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var request struct {
			ProbeOtherServers bool `json:"probe_other_servers"`
			TimeoutMS         *int `json:"timeout_ms"`
		}
		if err := diagnosticInput(input, &request); err != nil {
			return nil, err
		}
		timeout := 1000
		if request.TimeoutMS != nil {
			timeout = *request.TimeoutMS
		}
		if timeout < 100 || timeout > 3000 {
			return nil, fmt.Errorf("%w: timeout_ms must be 100..3000", control.BadRequest)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		settings, generation := storeSettings()
		status := service.DHCPStatus()
		result := map[string]any{"generation": strconv.FormatUint(generation, 10), "checked_at": time.Now().UTC(), "timeout_ms": timeout, "dhcp": safeJSON(status), "probe_requested": request.ProbeOtherServers, "other_servers": []string{}, "probe_truncated": false, "observation": "not_probed", "warning": "No offer observed does not prove that no other DHCP server exists. Quiet hosts may still conflict with the pool."}
		candidate := settings.Clone()
		candidate.Enabled = true
		checks := map[string]any{}
		result["checks"] = checks
		if err := dhcp.ValidateSettings(candidate); err != nil {
			checks["configuration"] = control.RedactMessage(err.Error())
		} else {
			checks["configuration"] = "valid"
		}
		if err := dhcp.ValidateDNS(candidate, service.Addresses().DNS); err != nil {
			checks["dns_listener"] = control.RedactMessage(err.Error())
		} else {
			checks["dns_listener"] = "bound configuration matches; firewall/LAN reachability not proven"
		}
		// An owned runtime already established these prerequisites. Opening a second
		// server socket would report our own listener as a false conflict.
		readiness, err := service.dhcpDiagnosticReadiness(ctx, candidate)
		if err != nil {
			readiness = control.RedactMessage(err.Error())
		}
		checks["interface_static_address_socket"] = readiness
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return result, err
		}
		checks["dns_ready"] = strconv.FormatBool(service.Ready())
		checks["pool_conflicts"] = "no exhaustive address scan; see configuration and runtime ownership errors; runtime probes new allocations"
		if request.ProbeOtherServers {
			probe, err := dhcp.ProbeServers(ctx, settings, time.Duration(timeout)*time.Millisecond)
			if probe.Servers == nil {
				probe.Servers = []string{}
			}
			result["other_servers"] = probe.Servers
			result["probe_truncated"] = probe.Truncated
			if err != nil {
				result["observation"] = "failed"
				return result, err
			}
			result["observation"] = "no_offer_observed"
			if len(probe.Servers) > 0 {
				result["observation"] = "offers_observed"
			}
		}
		return result, nil
	}
}

// Serialize temporary readiness sockets with runtime preparation. Otherwise an
// enable racing this explicit check could latch our own temporary bind as a
// server activation failure. The job owns the gate until all sockets are closed.
func (s *Service) dhcpDiagnosticReadiness(ctx context.Context, candidate dhcp.Settings) (string, error) {
	s.mu.Lock()
	supervisor := s.dhcp
	s.mu.Unlock()
	if supervisor == nil {
		return "", control.ErrUnavailable
	}
	select {
	case supervisor.gate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	supervisor.mu.RLock()
	owned, closed := supervisor.runtime != nil, supervisor.closed
	supervisor.mu.RUnlock()
	if closed {
		<-supervisor.gate
		return "", control.ErrUnavailable
	}
	if owned {
		<-supervisor.gate
		return "runtime-owned; inspect DHCP status and storage health", nil
	}
	if err := ctx.Err(); err != nil {
		<-supervisor.gate
		return "", err
	}
	// Like runtime preparation, a syscall may outlive its caller. Retain the
	// shared preparation gate and close the temporary link before releasing it;
	// repeated jobs cannot accumulate replacement workers or sockets.
	done := make(chan error, 1)
	go func() {
		defer func() { <-supervisor.gate }()
		link, _, err := supervisor.openLink(candidate)
		if err == nil {
			err = link.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
		return "ready at check time", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
