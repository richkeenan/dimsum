package config

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Upsert edits existing scalars or inserts missing block-mapping paths without
// reserializing existing text. The whole grouped candidate is validated once.
// Ambiguous comment boundaries and flow mappings still require explicit text.
func (d *Document) Upsert(edits []Edit) (*Document, error) {
	current := d
	seen := make(map[string]bool)
	for _, edit := range edits {
		if editableStringListPath(edit.Path) {
			value, err := stringListValue(edit.Value)
			if err != nil {
				return nil, err
			}
			edit.Value = value
		}
		if len(edit.Path) == 0 {
			return nil, fmt.Errorf("edit: empty path")
		}
		key := strings.Join(edit.Path, "\x00")
		if seen[key] {
			return nil, fmt.Errorf("edit: overlapping paths")
		}
		seen[key] = true
		for _, segment := range edit.Path {
			if strings.ContainsRune(segment, '\x00') {
				return nil, fmt.Errorf("edit: invalid path")
			}
		}
		var source []byte
		var err error
		if _, nodeErr := current.node(edit.Path); nodeErr == nil {
			if editableStringListPath(edit.Path) {
				source, err = current.editStringList(edit)
			} else {
				source, err = current.editSource([]Edit{edit})
			}
		} else {
			source, err = current.insertScalar(edit)
		}
		if err != nil {
			return nil, err
		}
		current = &Document{source: source}
		if err = yaml.Unmarshal(source, &current.root); err != nil {
			return nil, err
		}
	}
	return d.parseEdit(current.source)
}

func (d *Document) insertScalar(edit Edit) ([]byte, error) {
	n := d.root.Content[0]
	for depth, key := range edit.Path {
		if n.Kind == yaml.SequenceNode && n.Style&yaml.FlowStyle == 0 {
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(n.Content) {
				return nil, fmt.Errorf("edit %v: invalid sequence index", edit.Path)
			}
			n = n.Content[index]
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
		if next != nil {
			n = next
			continue
		}
		switch edit.Value.(type) {
		case string, int, int64, bool:
		case []string:
			if !editableStringListPath(edit.Path) {
				return nil, fmt.Errorf("edit: only discovery interfaces and bootstrap DNS accept sequences")
			}
		default:
			return nil, fmt.Errorf("edit: unsupported scalar %T", edit.Value)
		}
		var value any = edit.Value
		for i := len(edit.Path) - 1; i >= depth; i-- {
			value = map[string]any{edit.Path[i]: value}
		}
		var encoded bytes.Buffer
		encoder := yaml.NewEncoder(&encoded)
		encoder.SetIndent(2)
		if err := encoder.Encode(value); err != nil {
			return nil, err
		}
		if err := encoder.Close(); err != nil {
			return nil, err
		}
		if len(n.Content) > 0 && multiline(n.Content[len(n.Content)-1]) {
			return nil, fmt.Errorf("edit: multiline insertion boundary requires explicit text edit")
		}
		pos := lineStart(d.source, lastLine(n)+1)
		if err := appendBoundary(d.source, n, pos); err != nil {
			return nil, err
		}
		indent := max(0, n.Column-1)
		prefix := strings.Repeat(" ", indent)
		addition := prefix + strings.ReplaceAll(strings.TrimSuffix(encoded.String(), "\n"), "\n", "\n"+prefix) + "\n"
		if pos > 0 && d.source[pos-1] != '\n' {
			addition = "\n" + addition
		}
		out := make([]byte, 0, len(d.source)+len(addition))
		out = append(out, d.source[:pos]...)
		out = append(out, addition...)
		out = append(out, d.source[pos:]...)
		return out, nil
	}
	return nil, fmt.Errorf("edit: target is not an editable scalar")
}
