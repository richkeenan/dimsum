package admin

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/richkeenan/dimsum/internal/control"
)

func (s *Server) dhcpRoute(w http.ResponseWriter, r *http.Request, resource string) {
	var v any
	var err error
	reservations := resource == "dhcp/reservations" || strings.HasPrefix(resource, "dhcp/reservations/")
	id := strings.TrimPrefix(resource, "dhcp/reservations/")
	if resource == "dhcp/reservations" {
		id = ""
	}
	if r.Method == "GET" {
		switch resource {
		case "dhcp":
			v, err = s.service.DHCPConfig(false)
		case "dhcp/status":
			v, err = s.service.DHCPStatus()
		case "dhcp/leases":
			query, parseErr := url.ParseQuery(r.URL.RawQuery)
			if parseErr != nil {
				s.fail(w, r, http.StatusBadRequest, "bad_request", "malformed lease query: "+parseErr.Error())
				return
			}
			v, err = s.service.DHCPLeases(query)
		case "dhcp/reservations":
			v, err = s.service.DHCPConfig(true)
		default:
			s.fail(w, r, 404, "not_found", "unknown DHCP operation")
			return
		}
	} else {
		if !(resource == "dhcp" && r.Method == "PATCH" || reservations && id == "" && r.Method == "POST" || reservations && id != "" && !strings.Contains(id, "/") && (r.Method == "PATCH" || r.Method == "DELETE")) {
			s.fail(w, r, 405, "method_not_allowed", "unsupported DHCP operation")
			return
		}
		var m control.DHCPMutation
		if !decode(w, r, &m) {
			return
		}
		v, err = s.service.DHCPMutate(r.Context(), r.Method, id, reservations, m)
	}
	s.result(w, r, v, err)
}
