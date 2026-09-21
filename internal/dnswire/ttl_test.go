package dnswire_test

import (
	"encoding/binary"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTemplateTTLs(t *testing.T) {
	for _, negative := range []bool{false, true} {
		m := new(dns.Msg)
		m.SetQuestion("example.org.", dns.TypeA)
		m.Response, m.Compress = true, true
		a, err := dns.NewRR("example.org. 120 IN A 192.0.2.1")
		require.NoError(t, err)
		soa, err := dns.NewRR("example.org. 200 IN SOA ns.example.org. hostmaster.example.org. 1 2 3 4 60")
		require.NoError(t, err)
		if negative {
			m.Rcode = dns.RcodeNameError
			m.Ns = []dns.RR{soa}
		} else {
			m.Answer = []dns.RR{a}
			m.Ns = []dns.RR{soa}
		}
		m.SetEdns0(1232, true)
		wire, err := m.Pack()
		require.NoError(t, err)
		var patches [256]byte
		info, err := dnswire.PrepareTemplate(wire, patches[:], 30)
		require.NoError(t, err)
		assert.Equal(t, negative, info.Negative)
		want := uint32(120)
		if negative {
			want = 30
		}
		assert.Equal(t, want, info.Lifetime)
		out := append([]byte(nil), wire...)
		for i := 0; i < info.Patches; i++ {
			p := patches[i*8:]
			binary.BigEndian.PutUint32(out[binary.BigEndian.Uint16(p):], binary.BigEndian.Uint32(p[4:])-10)
		}
		var got dns.Msg
		require.NoError(t, got.Unpack(out))
		assert.EqualValues(t, 0x8000, got.IsEdns0().Hdr.Ttl)
		if negative {
			assert.EqualValues(t, 20, got.Ns[0].Header().Ttl)
		} else {
			assert.EqualValues(t, 110, got.Answer[0].Header().Ttl)
			assert.EqualValues(t, 190, got.Ns[0].Header().Ttl)
		}
	}
}

func TestTemplateRejectsMutableNameDependencies(t *testing.T) {
	for _, target := range []byte{1, 3, 23} {
		p := personalizationResponse()
		p[7] = 2
		p = appendPersonalizationRR(p, []byte{0xc0, 12}, 1, []byte{1, 2, 3, 4})
		p = appendPersonalizationRR(p, []byte{0xc0, target}, 1, []byte{1, 2, 3, 4})
		var original dnswire.Message
		require.NoError(t, dnswire.ScanMessage(p, &original))
		var patches [256]byte
		_, err := dnswire.PrepareTemplate(p, patches[:], 300)
		assert.Error(t, err)
	}
}
