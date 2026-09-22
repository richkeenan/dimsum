//go:build !linux

package dhcp

func DetectSetup(saved Settings) Setup {
	v := suggestSetup(saved, nil, nil)
	v.Message = "Automatic network detection is available on Linux. Open Edit settings to enter your network details."
	return v
}
