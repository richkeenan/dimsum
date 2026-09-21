package config

import (
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"slices"
	"strings"
)

func discoveryInterfacesPath(path []string) bool {
	return slices.Equal(path, []string{"naming", "mdns", "interfaces"})
}
func discoveryInterfacesValue(v any) ([]string, error) {
	var out []string
	switch values := v.(type) {
	case []string:
		out = slices.Clone(values)
	case []any:
		for _, value := range values {
			s, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("discovery interfaces must be strings")
			}
			out = append(out, s)
		}
	default:
		return nil, fmt.Errorf("discovery interfaces must be an array")
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
func (d *Document) editDiscoveryInterfaces(edit Edit) ([]byte, error) {
	parent, err := d.node([]string{"naming", "mdns"})
	if err != nil {
		return nil, err
	}
	var key, n *yaml.Node
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == "interfaces" {
			key, n = parent.Content[i], parent.Content[i+1]
			break
		}
	}
	if n == nil || n.Kind != yaml.SequenceNode || multiline(n) {
		return nil, fmt.Errorf("discovery interfaces require a simple sequence")
	}
	for _, child := range n.Content {
		if child.HeadComment != "" || child.LineComment != "" || child.FootComment != "" {
			return nil, fmt.Errorf("commented interface entries require explicit text editing")
		}
	}
	if n.FootComment != "" {
		return nil, fmt.Errorf("commented interface footer requires explicit text editing")
	}
	start, end := lineStart(d.source, key.Line), lineStart(d.source, lastLine(n)+1)
	headerEnd := lineStart(d.source, key.Line+1)
	header := string(d.source[start:headerEnd])
	colon := strings.IndexByte(header, ':')
	if colon < 0 {
		return nil, fmt.Errorf("invalid interface header")
	}
	encoded, err := json.Marshal(edit.Value)
	if err != nil {
		return nil, err
	}
	replacement := header[:colon+1] + " " + string(encoded)
	if comment := strings.Index(header, "#"); comment >= 0 {
		replacement += " " + strings.TrimSpace(header[comment:])
	}
	replacement += "\n"
	out := append([]byte(nil), d.source[:start]...)
	out = append(out, replacement...)
	out = append(out, d.source[end:]...)
	return out, nil
}
