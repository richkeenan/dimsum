package config

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
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

// AddAdminHost retains owner formatting, including flow lists and comments.
func (d *Document) AddAdminHost(host string) (*Document, error) {
	path := []string{"admin", "allowed_hosts"}
	n, err := d.node(path)
	if err == nil && n.Kind == yaml.SequenceNode && n.Style&yaml.FlowStyle != 0 && len(n.Content) > 0 {
		start := lineStart(d.source, n.Line) + n.Column - 1
		if start >= len(d.source) || d.source[start] != '[' {
			return nil, fmt.Errorf("admin.allowed_hosts: unsupported flow sequence")
		}
		encoded, err := json.Marshal(host)
		if err != nil {
			return nil, err
		}
		// Prepend inside the flow list so every existing byte is preserved.
		out := append([]byte(nil), d.source[:start+1]...)
		out = append(out, encoded...)
		out = append(out, ',', ' ')
		out = append(out, d.source[start+1:]...)
		return Parse(out)
	}
	return d.Append(path, host)
}
