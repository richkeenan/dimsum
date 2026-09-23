package policy

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseObservedName accepts Display's byte-safe DNS presentation, or a Unicode
// configuration hostname. ASCII labels are DNS bytes, not IDNA labels. Unicode
// input uses NormalizeName and cannot be mixed with decimal escapes. Empty and
// dot both denote the root, whose Display is empty.
func ParseObservedName(value string) (Name, error) {
	for i := 0; i < len(value); i++ {
		if value[i] >= 128 {
			return NormalizeName(value)
		}
	}
	if value == "" || value == "." {
		return Name{}, nil
	}
	value = strings.TrimSuffix(value, ".")
	var wire, label []byte
	appendLabel := func() error {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("policy: invalid observed name label length")
		}
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
		label = nil
		return nil
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '.':
			if err := appendLabel(); err != nil {
				return Name{}, err
			}
		case c == '\\':
			if i+3 >= len(value) {
				return Name{}, fmt.Errorf("policy: name escapes require three decimal digits")
			}
			digits := value[i+1 : i+4]
			for _, digit := range digits {
				if digit < '0' || digit > '9' {
					return Name{}, fmt.Errorf("policy: name escapes require three decimal digits")
				}
			}
			n, err := strconv.ParseUint(digits, 10, 8)
			if err != nil {
				return Name{}, fmt.Errorf("policy: name escape exceeds 255")
			}
			label = append(label, byte(n))
			i += 3
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			label = append(label, c)
		default:
			return Name{}, fmt.Errorf("policy: observed name bytes outside letters, digits, hyphen and underscore require decimal escapes")
		}
		if len(label) > 63 || len(wire)+1+len(label) > 254 {
			return Name{}, fmt.Errorf("policy: invalid observed name length")
		}
	}
	if err := appendLabel(); err != nil {
		return Name{}, err
	}
	return NameFromWire(append(wire, 0))
}
