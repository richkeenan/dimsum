package dnswire_test

import (
	"fmt"
	"testing"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNegativeTemplateFollowsCNAMEAuthority(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answers   []string
		extra     []string
		zone      string
		cacheable bool
	}{
		{"cross-zone", []string{"app.example.com. 120 IN CNAME edge.example.net."}, nil, "example.net.", true},
		{"same-zone", []string{"app.example.com. 120 IN CNAME edge.example.com."}, nil, "example.com.", true},
		{"multi-hop-out-of-order", []string{"middle.example.org. 90 IN CNAME edge.example.net.", "app.example.com. 120 IN CNAME middle.example.org."}, nil, "example.net.", true},
		{"mixed-case", []string{"APP.Example.COM. 120 IN CNAME EDGE.Example.NET."}, nil, "example.net.", true},
		{"duplicate-same-target", []string{"app.example.com. 120 IN CNAME edge.example.net.", "app.example.com. 120 IN CNAME EDGE.EXAMPLE.NET."}, nil, "example.net.", true},
		{"unrelated-soa", []string{"app.example.com. 120 IN CNAME edge.example.net."}, nil, "example.org.", false},
		{"original-zone-not-terminal", []string{"app.example.com. 120 IN CNAME edge.example.net."}, nil, "example.com.", false},
		{"unrelated-answer-alias", []string{"unrelated.example.com. 120 IN CNAME edge.example.net."}, nil, "example.net.", false},
		{"additional-alias-is-not-a-chain", nil, []string{"app.example.com. 120 IN CNAME edge.example.net."}, "example.net.", false},
		{"alias-loop", []string{"app.example.com. 120 IN CNAME edge.example.net.", "edge.example.net. 120 IN CNAME app.example.com."}, nil, "example.com.", false},
		{"conflicting-targets", []string{"app.example.com. 120 IN CNAME one.example.com.", "app.example.com. 120 IN CNAME two.example.com."}, nil, "example.com.", false},
		{"missing-soa", []string{"app.example.com. 120 IN CNAME edge.example.net."}, nil, "", false},
	} {
		for _, rcode := range []int{dns.RcodeSuccess, dns.RcodeNameError} {
			t.Run(fmt.Sprintf("%s/%s", tc.name, dns.RcodeToString[rcode]), func(t *testing.T) {
				m := new(dns.Msg)
				m.SetQuestion("app.example.com.", dns.TypeHTTPS)
				m.Response, m.Compress, m.RecursionAvailable = true, true, true
				m.Rcode = rcode
				for _, text := range tc.answers {
					rr, err := dns.NewRR(text)
					require.NoError(t, err)
					m.Answer = append(m.Answer, rr)
				}
				for _, text := range tc.extra {
					rr, err := dns.NewRR(text)
					require.NoError(t, err)
					m.Extra = append(m.Extra, rr)
				}
				if tc.zone != "" {
					rr, err := dns.NewRR(tc.zone + " 200 IN SOA ns.example.net. hostmaster.example.net. 1 3600 600 86400 60")
					require.NoError(t, err)
					m.Ns = []dns.RR{rr}
				}
				m.SetEdns0(1232, false)
				wire, err := m.Pack()
				require.NoError(t, err)
				var parsed dnswire.Message
				require.NoError(t, dnswire.ScanMessage(wire, &parsed), "fixture must be structurally valid DNS")
				var patches [512]byte
				info, err := dnswire.PrepareTemplate(wire, patches[:], 300)
				if !tc.cacheable {
					assert.ErrorIs(t, err, dnswire.ErrTemplate)
					return
				}
				require.NoError(t, err)
				assert.True(t, info.Negative)
				assert.EqualValues(t, 60, info.Lifetime, "the terminal SOA minimum bounds the negative lifetime")
			})
		}
	}
}

func TestNegativeTemplateBoundsCNAMEChain(t *testing.T) {
	for _, links := range []int{16, 17} {
		t.Run(fmt.Sprint(links), func(t *testing.T) {
			m := new(dns.Msg)
			m.SetQuestion("app.example.com.", dns.TypeHTTPS)
			m.Response, m.Compress = true, true
			owner := "app.example.com."
			for i := range links {
				target := fmt.Sprintf("n%d.example.net.", i)
				rr, err := dns.NewRR(owner + " 120 IN CNAME " + target)
				require.NoError(t, err)
				m.Answer = append(m.Answer, rr)
				owner = target
			}
			soa, err := dns.NewRR("example.net. 200 IN SOA ns.example.net. hostmaster.example.net. 1 3600 600 86400 60")
			require.NoError(t, err)
			m.Ns = []dns.RR{soa}
			wire, err := m.Pack()
			require.NoError(t, err)
			var patches [512]byte
			info, err := dnswire.PrepareTemplate(wire, patches[:], 300)
			if links == 17 {
				assert.ErrorIs(t, err, dnswire.ErrTemplate)
				return
			}
			require.NoError(t, err)
			assert.True(t, info.Negative)
			assert.EqualValues(t, 60, info.Lifetime)
		})
	}
}
