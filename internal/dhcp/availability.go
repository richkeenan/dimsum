package dhcp

import (
	"errors"
	"os"
	"runtime"
)

// Availability describes platform/deployment support, not runtime readiness.
// Supported Linux hosts still need a suitable interface, address and privileges.
type Availability struct {
	Supported bool   `json:"supported"`
	Code      string `json:"code"`
	Reason    string `json:"reason"`
}

// CurrentAvailability uses explicit deployment metadata because a Linux container
// cannot reliably infer whether its host is Docker Desktop or native Linux.
func CurrentAvailability() Availability {
	return deploymentAvailability(runtime.GOOS, os.Getenv("DIMSUM_DEPLOYMENT"))
}

func deploymentAvailability(goos, deployment string) Availability {
	if deployment == "docker-desktop" {
		return Availability{Code: "docker_desktop", Reason: "DHCP serving is unavailable in this Docker Desktop setup because the container cannot access your LAN’s DHCP broadcasts. Use your router for DHCP, or run dimsum on Linux with direct LAN access."}
	}
	if deployment != "" {
		return Availability{Code: "unknown_deployment", Reason: "DHCP serving is unavailable because DIMSUM_DEPLOYMENT has an unknown value. Use docker-desktop for Docker Desktop or leave it unset on a Linux host with direct LAN access."}
	}
	if goos != "linux" {
		platform := goos
		if goos == "darwin" {
			platform = "macOS"
		}
		return Availability{Code: "unsupported_platform", Reason: "DHCP serving is unavailable on " + platform + ". dimsum currently supports DHCP serving on Linux."}
	}
	return Availability{Supported: true, Code: "supported"}
}

func (a Availability) Check() error {
	if !a.Supported {
		return errors.New(a.Reason)
	}
	return nil
}
