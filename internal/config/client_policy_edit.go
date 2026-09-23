package config

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// PolicyField is a source-level operation. Reset removes a key (inherit), never
// writes null. Append adds one sequence item. Only the final document is validated.
type PolicyField struct {
	Path   []string `json:"path"`
	Value  any      `json:"value,omitempty"`
	Reset  bool     `json:"reset,omitempty"`
	Append bool     `json:"append,omitempty"`
}

func (d *Document) PolicyFields(fields []PolicyField) (*Document, error) {
	current := d
	for _, f := range fields {
		if len(f.Path) == 0 || f.Reset && (f.Value != nil || f.Append) || !f.Reset && f.Value == nil {
			return nil, fmt.Errorf("policy edit: invalid operation")
		}
		source, err := current.policyField(f)
		if err != nil {
			return nil, err
		}
		current = &Document{source: source}
		if err := yaml.Unmarshal(source, &current.root); err != nil {
			return nil, err
		}
	}
	return d.parseEdit(current.source)
}

func encodePolicyValue(v any) ([]byte, error) {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (d *Document) policySplice(start, end int, text string) []byte {
	out := append([]byte{}, d.source[:start]...)
	out = append(out, text...)
	return append(out, d.source[end:]...)
}

func (d *Document) policyField(f PolicyField) ([]byte, error) {
	n, err := d.node(f.Path)
	if err != nil {
		// Walk to the first missing mapping key. Sequence indexes must exist.
		for i := range f.Path {
			if _, e := d.node(f.Path[:i+1]); e == nil {
				continue
			}
			parent, e := d.node(f.Path[:i])
			if e != nil {
				return nil, e
			}
			if parent.Kind == yaml.MappingNode && parent.Style&yaml.FlowStyle != 0 && len(parent.Content) == 0 && i > 0 {
				if f.Reset {
					return d.Bytes(), nil
				}
				value := f.Value
				if f.Append {
					value = []any{value}
				}
				for j := len(f.Path) - 1; j >= i; j-- {
					value = map[string]any{f.Path[j]: value}
				}
				return d.replacePolicyField(f.Path[:i], parent, value, false)
			}
			if parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("policy edit: requires block mapping")
			}
			var found bool
			for j := 0; j < len(parent.Content); j += 2 {
				if parent.Content[j].Value == f.Path[i] {
					found = true
				}
			}
			if found {
				continue
			}
			if f.Reset {
				return d.Bytes(), nil
			}
			value := f.Value
			if f.Append {
				value = []any{value}
			}
			for j := len(f.Path) - 1; j >= i; j-- {
				value = map[string]any{f.Path[j]: value}
			}
			b, e := encodePolicyValue(value)
			if e != nil {
				return nil, e
			}
			pos := lineStart(d.source, lastLine(parent)+1)
			if e := appendBoundary(d.source, parent, pos); e != nil {
				return nil, e
			}
			prefix := strings.Repeat(" ", max(0, parent.Column-1))
			text := prefix + strings.ReplaceAll(strings.TrimSuffix(string(b), "\n"), "\n", "\n"+prefix) + "\n"
			if pos > 0 && d.source[pos-1] != '\n' {
				text = "\n" + text
			}
			return d.policySplice(pos, pos, text), nil
		}
		return nil, err
	}
	if f.Append {
		if n.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("policy append: expected sequence")
		}
		b, e := encodePolicyValue(f.Value)
		if e != nil {
			return nil, e
		}
		if n.Style&yaml.FlowStyle != 0 {
			if len(n.Content) != 0 {
				return nil, fmt.Errorf("policy append: nonempty flow sequence requires explicit text edit")
			}
			return d.replacePolicyField(f.Path, n, []any{f.Value}, false)
		}
		pos := lineStart(d.source, lastLine(n)+1)
		if e := appendBoundary(d.source, n, pos); e != nil {
			return nil, e
		}
		return d.policySplice(pos, pos, indentItem(b, n.Column-1)), nil
	}
	if !f.Reset && n.Kind == yaml.ScalarNode {
		switch f.Value.(type) {
		case string, bool, int, int64:
			return d.editSource([]Edit{{Path: f.Path, Value: f.Value}})
		}
	}
	return d.replacePolicyField(f.Path, n, f.Value, f.Reset)
}

// Replacement retains comment text as standalone comments at the same level.
// Existing subtrees are never marshaled. Ambiguous multiline/flow parents fail.
func (d *Document) replacePolicyField(path []string, n *yaml.Node, value any, reset bool) ([]byte, error) {
	parent, e := d.node(path[:len(path)-1])
	if e != nil {
		return nil, e
	}
	if parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0 || multiline(n) {
		return nil, fmt.Errorf("policy replacement requires a single-line/block mapping field")
	}
	var key *yaml.Node
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == path[len(path)-1] {
			key = parent.Content[i]
		}
	}
	if key == nil {
		return nil, fmt.Errorf("policy field not found")
	}
	start, end := lineStart(d.source, key.Line), lineStart(d.source, lastLine(n)+1)
	indent := strings.Repeat(" ", key.Column-1)
	// Preserve every comment-bearing source line. Inline comment positions come
	// from the parser, so '#' inside quoted strings is never mistaken for a comment.
	inline := map[int]string{}
	var visit func(*yaml.Node)
	visit = func(v *yaml.Node) {
		if v.LineComment != "" {
			inline[v.Line] = v.LineComment
		}
		for _, c := range v.Content {
			visit(c)
		}
	}
	visit(key)
	visit(n)
	var text strings.Builder
	first := string(d.source[start:lineStart(d.source, key.Line+1)])
	if strings.HasPrefix(strings.TrimSpace(first), "- ") {
		text.WriteString(strings.Repeat(" ", key.Column-3) + "-\n")
	}
	for line := key.Line; line <= lastLine(n); line++ {
		raw := string(d.source[lineStart(d.source, line):lineStart(d.source, line+1)])
		if strings.HasPrefix(strings.TrimSpace(raw), "#") {
			text.WriteString(raw)
		} else if c := inline[line]; c != "" {
			text.WriteString(indent + c + "\n")
		}
	}
	if !reset {
		b, e := encodePolicyValue(map[string]any{key.Value: value})
		if e != nil {
			return nil, e
		}
		text.WriteString(indent + strings.ReplaceAll(strings.TrimSuffix(string(b), "\n"), "\n", "\n"+indent) + "\n")
	} else if len(parent.Content) == 2 {
		// Keep an empty mapping syntactically valid after its last field is reset.
		text.WriteString(indent + "{}\n")
	}
	return d.policySplice(start, end, text.String()), nil
}
