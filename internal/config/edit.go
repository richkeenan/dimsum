package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Edit performs a grouped, validated edit of existing single-line scalar values
// in block mappings. Unsupported shapes fail rather than reformat user content.
// Collection insertion/deletion is deliberately left to later control tasks.
func (d *Document) Edit(edits []Edit) (*Document, error) {
	for _, edit := range edits {
		editable, err := d.editableUpstreams(edit.Path)
		if err != nil {
			return nil, err
		}
		if editable != d {
			return editable.Edit(edits)
		}
	}
	out, err := d.editSource(edits)
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

func (d *Document) editSource(edits []Edit) ([]byte, error) {
	type splice struct {
		start, end int
		text       []byte
	}
	var changes []splice
	for _, edit := range edits {
		n := d.root.Content[0]
		if len(edit.Path) == 0 {
			return nil, fmt.Errorf("edit: empty path")
		}
		for _, key := range edit.Path {
			if n.Kind == yaml.SequenceNode && n.Style&yaml.FlowStyle == 0 {
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(n.Content) {
					return nil, fmt.Errorf("edit %v: invalid sequence index", edit.Path)
				}
				n = n.Content[i]
				continue
			}
			if n.Kind != yaml.MappingNode || n.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("edit %v: requires block mapping", edit.Path)
			}
			var next *yaml.Node
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == key {
					next = n.Content[i+1]
					break
				}
			}
			if next == nil {
				return nil, fmt.Errorf("edit %v: missing key", edit.Path)
			}
			n = next
		}
		if n.Kind != yaml.ScalarNode || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.Contains(n.Value, "\n") {
			return nil, fmt.Errorf("edit %v: requires single-line scalar", edit.Path)
		}
		switch edit.Value.(type) {
		case string, int, int64, bool:
		default:
			return nil, fmt.Errorf("edit %v: unsupported value type %T", edit.Path, edit.Value)
		}
		text, err := json.Marshal(edit.Value)
		if err != nil {
			return nil, err
		}
		start, end, err := scalarSpan(d.source, n)
		if err != nil {
			return nil, err
		}
		changes = append(changes, splice{start, end, text})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].start < changes[j].start })
	var out []byte
	pos := 0
	for _, c := range changes {
		if c.start < pos {
			return nil, fmt.Errorf("edit: overlapping paths")
		}
		out = append(out, d.source[pos:c.start]...)
		out = append(out, c.text...)
		pos = c.end
	}
	out = append(out, d.source[pos:]...)
	return out, nil
}

func scalarSpan(source []byte, n *yaml.Node) (int, int, error) {
	start := 0
	for line := 1; line < n.Line; line++ {
		i := strings.IndexByte(string(source[start:]), '\n')
		if i < 0 {
			return 0, 0, fmt.Errorf("edit: invalid source position")
		}
		start += i + 1
	}
	for col := 1; col < n.Column; col++ {
		_, size := utf8.DecodeRune(source[start:])
		start += size
	}
	end := start
	if source[start] == '\'' || source[start] == '"' {
		quote := source[start]
		end++
		for end < len(source) && source[end] != '\n' && source[end] != '\r' {
			if quote == '"' && source[end] == '\\' {
				end += 2
				continue
			}
			if source[end] == quote {
				if quote == '\'' && end+1 < len(source) && source[end+1] == '\'' {
					end += 2
					continue
				}
				return start, end + 1, nil
			}
			end++
		}
		return 0, 0, fmt.Errorf("edit: multiline quoted scalar unsupported")
	}
	for end < len(source) && source[end] != '\n' && source[end] != '\r' {
		if source[end] == '#' && (end == start || source[end-1] == ' ' || source[end-1] == '\t') {
			break
		}
		end++
	}
	for end > start && (source[end-1] == ' ' || source[end-1] == '\t') {
		end--
	}
	// Plain scalars can continue on later lines. Refuse those rather than leave
	// continuation text behind; decoding the token checks its entire meaning.
	var value yaml.Node
	if err := yaml.Unmarshal(source[start:end], &value); err != nil || len(value.Content) != 1 || value.Content[0].Value != n.Value {
		return 0, 0, fmt.Errorf("edit: multiline scalar unsupported")
	}
	return start, end, nil
}
