package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

func (d *Document) policyNode(path []string) (*yaml.Node, error) {
	n := d.root.Content[0]
	for _, key := range path {
		if n.Kind == yaml.SequenceNode {
			i, e := strconv.Atoi(key)
			if e != nil || i < 0 || i >= len(n.Content) {
				return nil, fmt.Errorf("invalid sequence index")
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
			return nil, fmt.Errorf("missing policy field %s", key)
		}
		n = next
	}
	return n, nil
}
func (d *Document) policyPosition(n *yaml.Node) int {
	pos := lineStart(d.source, n.Line)
	for col := 1; col < n.Column; col++ {
		_, size := utf8.DecodeRune(d.source[pos:])
		pos += size
	}
	return pos
}

// flowEnd scans a single YAML flow token; multiline/commented flow shapes are
// rejected rather than guessing where existing comments belong.
func (d *Document) flowEnd(n *yaml.Node) (int, error) {
	start := d.policyPosition(n)
	depth := 0
	var quote byte
	for i := start; i < len(d.source); i++ {
		c := d.source[i]
		if c == '\n' || c == '\r' {
			return 0, fmt.Errorf("multiline flow edit requires explicit text edit")
		}
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(d.source) && d.source[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
				if depth == 0 {
					return i + 1, nil
				}
			}
			continue
		}
		if c == '#' && (i == start || d.source[i-1] == ' ') {
			return 0, fmt.Errorf("commented flow edit requires explicit text edit")
		}
		switch c {
		case '\'', '"':
			if i == start || depth > 0 {
				quote = c
			}
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return start + len(strings.TrimRight(string(d.source[start:i]), " \t")), nil
			}
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		case ',':
			if depth == 0 {
				return start + len(strings.TrimRight(string(d.source[start:i]), " \t")), nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated flow token")
}

func flowValue(value any) (string, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return "", err
	}
	var flow func(*yaml.Node)
	flow = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
			n.Style |= yaml.FlowStyle
		}
		for _, c := range n.Content {
			flow(c)
		}
	}
	flow(&node)
	b, err := yaml.Marshal(&node)
	return strings.TrimSuffix(string(b), "\n"), err
}

func (d *Document) policyFlowField(parent *yaml.Node, key string, value any, reset bool) ([]byte, error) {
	close, err := d.flowEnd(parent)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			index = i
			break
		}
	}
	if index < 0 {
		if reset {
			return d.Bytes(), nil
		}
		token, e := flowValue(map[string]any{key: value})
		if e != nil {
			return nil, e
		}
		token = strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
		if len(parent.Content) > 0 {
			token = ", " + token
		}
		return d.policySplice(close-1, close-1, token), nil
	}
	n := parent.Content[index+1]
	start := d.policyPosition(n)
	end, e := d.flowEnd(n)
	if e != nil {
		return nil, e
	}
	if !reset {
		token, e := flowValue(value)
		if e != nil {
			return nil, e
		}
		return d.policySplice(start, end, token), nil
	}
	start = d.policyPosition(parent.Content[index])
	if index+2 < len(parent.Content) {
		end = d.policyPosition(parent.Content[index+2])
	} else if index > 0 {
		start, e = d.flowEnd(parent.Content[index-1])
		if e != nil {
			return nil, e
		}
	}
	return d.policySplice(start, end, ""), nil
}
