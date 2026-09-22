//go:build !linux

package dhcp

func OpenSystemLink(Settings) (Link, ProbeFunc, error) {
	return nil, nil, CurrentAvailability().Check()
}
