package config

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// editableUpstreams expands an ordinary single-line upstream list in place.
// Only the list token changes; header comments and unrelated text stay intact.
func (d *Document) editableUpstreams(path []string) (*Document, error) {
	if len(path) < 2 || path[0] != "dns" || path[1] != "upstreams" {
		return d, nil
	}
	n, err := d.node([]string{"dns", "upstreams"})
	if err != nil || n.Kind != yaml.SequenceNode || n.Style&yaml.FlowStyle == 0 || len(n.Content) == 0 {
		return d, nil
	}
	start := lineStart(d.source, n.Line)
	end := lineStart(d.source, n.Line+1)
	line := string(d.source[start:end])
	// YAML columns count Unicode characters, not bytes.
	at := len(string([]rune(line)[:n.Column-1]))
	if n.Anchor != "" || at >= len(line) || line[at] != '[' {
		return nil, fmt.Errorf("upstream list uses an anchor or tag; use a plain list of IP addresses and ports")
	}
	quote := byte(0)
	close := -1
	for i := at + 1; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if quote == '"' && c == '\\' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '#' {
			break
		}
		if c == ']' {
			close = i
			break
		}
	}
	if close < 0 {
		return nil, fmt.Errorf("upstream list spans multiple lines; put each server on its own '- IP:port' line before editing")
	}
	values := make([]string, len(n.Content))
	for i, child := range n.Content {
		if child.Kind != yaml.ScalarNode || child.Anchor != "" || child.Line != n.Line {
			return nil, fmt.Errorf("upstream list must contain plain IP addresses and ports")
		}
		values[i] = child.Value
	}
	indent := len(line) - len(strings.TrimLeft(line, " ")) + 2
	header := line[:at] + line[close+1:]
	if !strings.HasSuffix(header, "\n") {
		header += "\n"
	}
	var block strings.Builder
	block.WriteString(header)
	for _, value := range values {
		encoded, err := yaml.Marshal(value)
		if err != nil {
			return nil, err
		}
		block.WriteString(indentItem(encoded, indent))
	}
	out := append([]byte(nil), d.source[:start]...)
	out = append(out, block.String()...)
	out = append(out, d.source[end:]...)
	return Parse(out)
}
