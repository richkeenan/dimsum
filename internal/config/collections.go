package config

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func (d *Document) node(path []string) (*yaml.Node, error) {
	n := d.root.Content[0]
	for _, key := range path {
		if n.Style&yaml.FlowStyle != 0 {
			return nil, fmt.Errorf("collection edit: flow collections require explicit text edit")
		}
		if n.Kind == yaml.SequenceNode {
			i, e := strconv.Atoi(key)
			if e != nil || i < 0 || i >= len(n.Content) {
				return nil, fmt.Errorf("collection edit: invalid index")
			}
			n = n.Content[i]
			continue
		}
		var next *yaml.Node
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == key {
					next = n.Content[i+1]
					break
				}
			}
		}
		if next == nil {
			return nil, fmt.Errorf("collection edit: missing %s", key)
		}
		n = next
	}
	return n, nil
}

func lineStart(b []byte, line int) int {
	pos := 0
	for i := 1; i < line; i++ {
		j := bytes.IndexByte(b[pos:], '\n')
		if j < 0 {
			return len(b)
		}
		pos += j + 1
	}
	return pos
}
func lastLine(n *yaml.Node) int {
	line := n.Line
	for _, c := range n.Content {
		line = max(line, lastLine(c))
	}
	return line
}

// Append inserts only newly encoded text; existing collections are never
// serialized. Block collections are supported; ambiguous shapes fail explicitly.
func (d *Document) Append(path []string, value any) (*Document, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("append: empty path")
	}
	n, err := d.node(path)
	b, errEncode := yaml.Marshal(value)
	if errEncode != nil {
		return nil, errEncode
	}
	if err != nil {
		if len(path) != 1 {
			return nil, err
		}
		for i := 0; i < len(d.root.Content[0].Content); i += 2 {
			if d.root.Content[0].Content[i].Value == path[0] {
				return nil, err
			}
		}
		switch path[0] {
		case "lists", "rules", "records", "clients":
		default:
			return nil, err
		}
		out := append(d.Bytes(), []byte("\n"+path[0]+":\n"+indentItem(b, 2))...)
		return Parse(out)
	}
	if n.Kind != yaml.SequenceNode || n.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("append: requires block sequence")
	}
	pos := lineStart(d.source, lastLine(n)+1)
	text := indentItem(b, n.Column-1)
	out := append([]byte(nil), d.source[:pos]...)
	if pos > 0 && out[pos-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, text...)
	out = append(out, d.source[pos:]...)
	return Parse(out)
}
func indentItem(b []byte, spaces int) string {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var out strings.Builder
	for i, line := range lines {
		out.WriteString(strings.Repeat(" ", spaces))
		if i == 0 {
			out.WriteString("- ")
		} else {
			out.WriteString("  ")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// Remove deletes a block-sequence item. Comment-bearing items are rejected,
// rather than silently dropping or reassociating comments the owner wrote.
func (d *Document) Remove(path []string) (*Document, error) {
	if len(path) < 2 {
		return nil, fmt.Errorf("remove: expected sequence item")
	}
	parent, e := d.node(path[:len(path)-1])
	if e != nil {
		return nil, e
	}
	if parent.Kind != yaml.SequenceNode || parent.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("remove: requires block sequence")
	}
	n, e := d.node(path)
	if e != nil {
		return nil, e
	}
	var comments func(*yaml.Node) bool
	comments = func(n *yaml.Node) bool {
		if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
			return true
		}
		for _, c := range n.Content {
			if comments(c) {
				return true
			}
		}
		return false
	}
	if comments(n) {
		return nil, fmt.Errorf("remove: comment-bearing item requires explicit text edit")
	}
	start, end := lineStart(d.source, n.Line), lineStart(d.source, lastLine(n)+1)
	out := append([]byte(nil), d.source[:start]...)
	if len(parent.Content) == 1 {
		out = append(out, []byte(strings.Repeat(" ", parent.Column-1)+"[]\n")...)
	}
	out = append(out, d.source[end:]...)
	return Parse(out)
}
