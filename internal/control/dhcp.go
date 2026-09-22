package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
)

var ErrLeaseCursor = errors.New("restart lease pagination")

func (s *Service) dhcpAvailability() dhcp.Availability {
	if s.options.DHCPAvailability != nil {
		return s.options.DHCPAvailability()
	}
	return dhcp.CurrentAvailability()
}

func (s *Service) checkDHCPAvailability(d *config.Document) error {
	if d.Config().DHCP.Enabled {
		if err := s.dhcpAvailability().Check(); err != nil {
			return fmt.Errorf("%w: %v", BadRequest, err)
		}
	}
	return nil
}

// DHCPStatus is passive: it never opens lease storage or sends packets.
func (s *Service) DHCPStatus() (any, error) {
	a, err := s.Status()
	if err != nil {
		return nil, err
	}
	var runtime any
	if s.options.DHCPStatus != nil {
		runtime = s.options.DHCPStatus()
	}
	return map[string]any{"status": a, "dhcp": runtime, "runtime_available": runtime != nil, "availability": s.dhcpAvailability()}, nil
}

func (s *Service) DHCPConfig(reservations bool) (any, error) {
	a, err := s.Status()
	if err != nil {
		return nil, err
	}
	d, err := s.documentAt(a.SavedRevision)
	if err != nil {
		return nil, err
	}
	c := d.Config().DHCP
	if c.Reservations == nil {
		c.Reservations = []dhcp.Reservation{}
	}
	if reservations {
		return map[string]any{"status": a, "items": c.Reservations}, nil
	}
	availability := s.dhcpAvailability()
	setup := dhcp.Setup{Config: c, Suggested: []string{}, FixedAddress: "unknown", Message: availability.Reason}
	if availability.Supported {
		setup = dhcp.DetectSetup(c)
	}
	return map[string]any{"status": a, "config": c, "setup": setup, "availability": availability}, nil
}

type DHCPMutation struct {
	Revision string            `json:"revision"`
	Edits    []config.Edit     `json:"edits,omitempty"`
	Item     *dhcp.Reservation `json:"item,omitempty"`
}

// DHCPMutate uses the same comment-preserving document and coordinator as settings.
// Reservation updates address a stable ID; scalar edits cannot escape their scope.
func (s *Service) DHCPMutate(ctx context.Context, method, id string, reservations bool, m DHCPMutation) (any, error) {
	if s.options.Store == nil {
		return nil, ErrUnavailable
	}
	if m.Revision == "" {
		return nil, fmt.Errorf("revision is required: %w", BadRequest)
	}
	d, err := s.documentAt(m.Revision)
	if err != nil {
		return nil, err
	}
	prefix := []string{"dhcp"}
	if reservations && id != "" {
		index := -1
		for i, r := range d.Config().DHCP.Reservations {
			if r.ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("reservation %q: %w", id, NotFound)
		}
		prefix = append(prefix, "reservations", strconv.Itoa(index))
	}
	switch {
	case method == "PATCH" && (!reservations || id != ""):
		if m.Item != nil || len(m.Edits) == 0 || len(m.Edits) > 16 {
			return nil, fmt.Errorf("PATCH requires 1..16 scalar edits only: %w", BadRequest)
		}
		allowed := map[string]bool{"enabled": true, "interface": true, "server_ip": true, "subnet": true, "gateway": true, "range_start": true, "range_end": true, "lease_seconds": true, "local_domain": true, "max_leases": true}
		if reservations {
			allowed = map[string]bool{"mac": true, "client_id": true, "address": true, "hostname": true}
		}
		edits := make([]config.Edit, len(m.Edits))
		seen := map[string]bool{}
		for i, e := range m.Edits {
			if len(e.Path) != 1 || !allowed[e.Path[0]] || seen[e.Path[0]] {
				return nil, fmt.Errorf("unsupported or repeated DHCP scalar path: %w", BadRequest)
			}
			seen[e.Path[0]] = true
			edits[i] = config.Edit{Path: append(append([]string{}, prefix...), e.Path...), Value: e.Value}
		}
		if err = normalizeEdits(edits); err != nil {
			return nil, fmt.Errorf("%w: %v", BadRequest, err)
		}
		for _, edit := range edits {
			field := edit.Path[len(edit.Path)-1]
			valid := false
			switch field {
			case "enabled":
				_, valid = edit.Value.(bool)
			case "lease_seconds", "max_leases":
				switch edit.Value.(type) {
				case int, int64:
					valid = true
				}
			default:
				_, valid = edit.Value.(string)
			}
			if !valid {
				return nil, fmt.Errorf("wrong scalar type for %s: %w", field, BadRequest)
			}
		}
		d, err = d.Upsert(edits)
	case method == "POST" && reservations && id == "":
		if m.Item == nil || len(m.Edits) != 0 {
			return nil, fmt.Errorf("POST requires item only: %w", BadRequest)
		}
		if len(d.Config().DHCP.Reservations) >= d.Config().DHCP.Capacity() {
			return nil, fmt.Errorf("reservation capacity reached: %w", ErrBusy)
		}
		var item any
		item, err = shape(*m.Item)
		if err == nil {
			candidate, appendErr := d.Append([]string{"dhcp", "reservations"}, item)
			if appendErr != nil && len(d.Config().DHCP.Reservations) == 0 {
				candidate, appendErr = insertMissingField(d, "dhcp", "reservations", []any{item})
			}
			d, err = candidate, appendErr
		}
	case method == "DELETE" && reservations && id != "":
		if m.Item != nil || len(m.Edits) != 0 {
			return nil, fmt.Errorf("DELETE requires revision only: %w", BadRequest)
		}
		d, err = d.Remove(prefix)
	default:
		return nil, fmt.Errorf("unsupported DHCP mutation: %w", BadRequest)
	}
	if err != nil {
		return nil, err
	}
	if err := s.checkDHCPAvailability(d); err != nil {
		return nil, err
	}
	a, err := s.options.Store.Save(ctx, m.Revision, d)
	return activation(a), err
}

type DHCPLease struct {
	Address   string          `json:"address"`
	MAC       string          `json:"mac"`
	ClientID  string          `json:"client_id,omitempty"`
	Hostname  string          `json:"hostname"`
	State     dhcp.LeaseState `json:"state"`
	Expiry    time.Time       `json:"expiry"`
	HoldUntil time.Time       `json:"hold_until"`
}
type DHCPLeasePage struct {
	BootID           string      `json:"boot_id"`
	Generation       string      `json:"generation"`
	Revision         string      `json:"revision"`
	RuntimeAvailable bool        `json:"runtime_available"`
	Items            []DHCPLease `json:"items"`
	NextCursor       string      `json:"next_cursor,omitempty"`
}
type leaseCursor struct {
	Boot, Generation, Revision, Config, Filter string
	Offset                                     int
}

func (s *Service) DHCPLeases(q url.Values) (DHCPLeasePage, error) {
	bad := func(message string) (DHCPLeasePage, error) {
		return DHCPLeasePage{}, fmt.Errorf("%s: %w", message, BadRequest)
	}
	for k, v := range q {
		if len(v) != 1 || (k != "limit" && k != "cursor" && k != "state" && k != "address" && k != "mac" && k != "client_id" && k != "hostname") {
			return bad("unknown or repeated lease filter")
		}
	}
	limit := 100
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 256 {
			return bad("limit must be 1..256")
		}
		limit = n
	}
	state := q.Get("state")
	if state != "" && state != "bound" && state != "offered" && state != "probing" && state != "commit-pending" && state != "quarantined" {
		return bad("invalid lease state")
	}
	address := q.Get("address")
	if address != "" {
		a, e := netip.ParseAddr(address)
		if e != nil || !a.Is4() {
			return bad("address must be IPv4")
		}
		address = a.String()
	}
	mac := q.Get("mac")
	if mac != "" {
		m, e := net.ParseMAC(mac)
		if e != nil || len(m) != 6 {
			return bad("mac must be Ethernet")
		}
		mac = m.String()
	}
	client := strings.ToLower(q.Get("client_id"))
	if client != "" {
		b, e := hex.DecodeString(client)
		if e != nil || len(b) > 255 {
			return bad("client_id must be at most 255 hex bytes")
		}
	}
	hostname := q.Get("hostname")
	if len(hostname) > 63 {
		return bad("hostname must be at most 63 bytes")
	}
	if len(q.Get("cursor")) > 2048 {
		return bad("cursor too long")
	}
	a, err := s.Status()
	if err != nil {
		return DHCPLeasePage{}, err
	}
	snap := dhcp.LeaseSnapshot{}
	if s.options.DHCPInspect != nil {
		snap = s.options.DHCPInspect()
	}
	if len(snap.Leases) > dhcp.MaximumLeases {
		return DHCPLeasePage{}, ErrBusy
	}
	page := DHCPLeasePage{BootID: s.options.BootID, Generation: strconv.FormatUint(snap.Generation, 10), Revision: strconv.FormatUint(snap.Revision, 10), RuntimeAvailable: snap.Generation != 0, Items: []DHCPLease{}}
	filterBytes, _ := json.Marshal([]string{state, address, mac, client, hostname, strconv.Itoa(limit)})
	digest := sha256.Sum256(filterBytes)
	cur := leaseCursor{Boot: page.BootID, Generation: page.Generation, Revision: page.Revision, Config: a.ActiveGeneration, Filter: hex.EncodeToString(digest[:])}
	offset := 0
	if raw := q.Get("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil {
			return bad("invalid lease cursor")
		}
		var old leaseCursor
		if json.Unmarshal(b, &old) != nil || old.Offset < 1 {
			return bad("invalid lease cursor")
		}
		offset = old.Offset
		old.Offset = 0
		if old != cur {
			return DHCPLeasePage{}, ErrLeaseCursor
		}
	}
	// Detached bounded snapshot, no SQL transaction or retained per-client pagination state.
	sort.Slice(snap.Leases, func(i, j int) bool { return snap.Leases[i].Address.Compare(snap.Leases[j].Address) < 0 })
	// Filter the bounded snapshot, but format only the requested page. Large
	// tables must not allocate identities/addresses for rows that are discarded.
	page.Items = make([]DHCPLease, 0, min(limit, len(snap.Leases)))
	matched := 0
	for _, l := range snap.Leases {
		if state != "" && state != string(l.State) || address != "" && address != l.Address.String() || mac != "" && mac != net.HardwareAddr(l.MAC[:]).String() || client != "" && (!strings.HasPrefix(l.Identity, "id:") || client != hex.EncodeToString([]byte(l.Identity[3:]))) || hostname != "" && hostname != l.Hostname {
			continue
		}
		matched++
		if matched <= offset || len(page.Items) == limit {
			continue
		}
		r := DHCPLease{Address: l.Address.String(), MAC: net.HardwareAddr(l.MAC[:]).String(), Hostname: l.Hostname, State: l.State, Expiry: l.Expiry.UTC(), HoldUntil: l.HoldUntil.UTC()}
		if strings.HasPrefix(l.Identity, "id:") {
			r.ClientID = hex.EncodeToString([]byte(l.Identity[3:]))
		}
		page.Items = append(page.Items, r)
	}
	if offset > matched {
		return bad("cursor offset outside lease snapshot")
	}
	end := offset + len(page.Items)
	if end < matched {
		cur.Offset = end
		b, _ := json.Marshal(cur)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}
