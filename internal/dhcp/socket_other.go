//go:build !linux

package dhcp

import "fmt"

func OpenSystemLink(Settings) (Link, ProbeFunc, error) {
	return nil, nil, fmt.Errorf("dhcp: packet service requires Linux")
}
