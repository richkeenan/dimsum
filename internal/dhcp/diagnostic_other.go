//go:build !linux

package dhcp

import (
	"fmt"
	"net"
)

func openDiagnosticSocket(string) (*net.UDPConn, error) {
	return nil, fmt.Errorf("DHCP server probe requires Linux")
}
