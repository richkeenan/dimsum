package dnswire

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeName(t *testing.T) {
	max := append(bytes.Repeat(append([]byte{63}, bytes.Repeat([]byte{'A'}, 63)...), 3), append([]byte{61}, bytes.Repeat([]byte{'B'}, 61)...)...)
	max = append(max, 0)
	tooLong := append(bytes.Clone(max[:192]), append([]byte{62}, bytes.Repeat([]byte{'B'}, 62)...)...)
	tooLong = append(tooLong, 0)
	chain := []byte{0}
	for i := 0; i < 33; i++ {
		target := len(chain) - 2
		if i == 0 {
			target = 0
		}
		chain = append(chain, 0xc0, byte(target))
	}
	tests := []struct {
		name      string
		wire      []byte
		off, end  int
		canonical []byte
		err       error
	}{
		{"root", []byte{0}, 0, 1, []byte{0}, nil},
		{"binary-dot", []byte{4, 'A', '.', 0xff, 0, 2, '_', 'B', 0}, 0, 9, []byte{4, 'a', '.', 0xff, 0, 2, '_', 'b', 0}, nil},
		{"label63", append(append([]byte{63}, bytes.Repeat([]byte{'A'}, 63)...), 0), 0, 65, append(append([]byte{63}, bytes.Repeat([]byte{'a'}, 63)...), 0), nil},
		{"name255", max, 0, 255, bytes.ToLower(max), nil},
		{"name256", tooLong, 0, 0, nil, ErrNameTooLong},
		// RFC 1035 section 4.1.4: F.ISI.ARPA, FOO.F.ISI.ARPA, ARPA.
		{"rfc-pointer", []byte{1, 'F', 3, 'I', 'S', 'I', 4, 'A', 'R', 'P', 'A', 0, 3, 'F', 'O', 'O', 0xc0, 0}, 12, 18, []byte{3, 'f', 'o', 'o', 1, 'f', 3, 'i', 's', 'i', 4, 'a', 'r', 'p', 'a', 0}, nil},
		{"nested", []byte{1, 'A', 0, 0xc0, 0, 0xc0, 3}, 5, 7, []byte{1, 'a', 0}, nil},
		{"empty", nil, 0, 0, nil, ErrBounds},
		{"negative", []byte{0}, -1, 0, nil, ErrBounds},
		{"past-end", []byte{0}, 1, 0, nil, ErrBounds},
		{"truncated-label", []byte{2, 'a'}, 0, 0, nil, ErrBounds},
		{"missing-root", []byte{1, 'a'}, 0, 0, nil, ErrBounds},
		{"truncated-pointer", []byte{0xc0}, 0, 0, nil, ErrBounds},
		{"reserved01", []byte{0x40, 0}, 0, 0, nil, ErrLabel},
		{"reserved10", []byte{0x80, 0}, 0, 0, nil, ErrLabel},
		{"self", []byte{0xc0, 0}, 0, 0, nil, ErrPointer},
		{"cycle", []byte{0xc0, 2, 0xc0, 0}, 2, 0, nil, ErrPointer},
		{"outside", []byte{0xc0, 255}, 0, 0, nil, ErrPointer},
		{"forward", []byte{0xc0, 2, 0}, 0, 0, nil, ErrPointer},
		{"invalid-target", []byte{0x80, 0, 0xc0, 0}, 2, 0, nil, ErrLabel},
		{"label-pointer-cycle", []byte{1, 'a', 0xc0, 0}, 0, 0, nil, ErrPointer},
		{"traversal", chain, len(chain) - 2, 0, nil, ErrPointer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := bytes.Clone(tt.wire)
			var n Name
			err := DecodeName(tt.wire, tt.off, &n)
			require.ErrorIs(t, err, tt.err)
			assert.Equal(t, before, tt.wire, "mutated input")
			if err == nil {
				assert.Equal(t, tt.end, n.End)
				assert.Equal(t, tt.canonical, n.Canonical[:n.Length])
				checkNameOracle(t, tt.wire, tt.off, &n)
			}
		})
	}
}

func TestNameBoundariesAndOwnership(t *testing.T) {
	var a, b Name
	wa, wb := []byte{3, 'A', '.', 'B', 0}, []byte{1, 'A', 1, 'B', 0}
	require.NoError(t, DecodeName(wa, 0, &a))
	require.NoError(t, DecodeName(wb, 0, &b))
	assert.NotEqual(t, a.Canonical[:a.Length], b.Canonical[:b.Length], "label boundaries merged")
	wa[1] = 'Z'
	assert.Equal(t, byte('A'), a.Wire[1], "name borrowed input")
	assert.Equal(t, byte('a'), a.Canonical[1], "name borrowed input")
}

func TestCompressionAndMessageLimits(t *testing.T) {
	// Exercise both pointer target octets and the exact traversal boundary.
	p := make([]byte, 300)
	p[256], p[257], p[258] = 1, 'Z', 0
	p[298], p[299] = 0xc1, 0
	var n Name
	require.NoError(t, DecodeName(p, 298, &n), "14-bit pointer")
	assert.Equal(t, 300, n.End)
	assert.Equal(t, []byte{1, 'z', 0}, n.Canonical[:n.Length])
	p = []byte{0}
	for i := 0; i < 32; i++ {
		target := len(p) - 2
		if i == 0 {
			target = 0
		}
		p = append(p, 0xc0, byte(target))
	}
	require.NoError(t, DecodeName(p, 63, &n), "32 pointers")
	assert.Equal(t, uint16(1), n.Length)
	assert.Equal(t, 65, n.End)
	p = make([]byte, 65535)
	require.NoError(t, DecodeName(p, 65534, &n), "last legal byte")
	assert.Equal(t, 65535, n.End)
	assert.ErrorIs(t, DecodeName(append(p, 0), 0, &n), ErrBounds, "oversize")
}
