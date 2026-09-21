package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func validateAdmin(a Admin) error {
	if len(a.AllowedHosts) > 16 {
		return fmt.Errorf("admin.allowed_hosts: at most 16 hosts")
	}
	for _, host := range a.AllowedHosts {
		h, p, err := net.SplitHostPort(host)
		port, pe := strconv.Atoi(p)
		if err != nil || pe != nil || h == "" || len(host) > 512 || port < 1 || port > 65535 || strings.ContainsAny(host, "/\\\r\n\t ") {
			return fmt.Errorf("admin.allowed_hosts: require exact hostname:port values")
		}
	}
	if strings.ContainsRune(a.ControlSocket, 0) || len(a.ControlSocket) > 4096 {
		return fmt.Errorf("admin.control_socket: invalid path")
	}
	return nil
}
