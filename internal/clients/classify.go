package clients

import "strings"

func classifyDevice(name string, evidence []Evidence) (string, string, bool) {
	category, reason, score := "unknown", "No specific device-type evidence", 0
	conflict := false
	add := func(c, r string, s int) {
		if s > score {
			category, reason, score, conflict = c, r, s, false
		} else if s == score && c != category {
			conflict = true
		}
	}
	for _, e := range evidence {
		switch strings.ToLower(e.DeviceType) {
		case "phone", "tablet", "laptop", "desktop", "tv", "speaker", "printer", "camera", "lighting", "appliance", "server":
			add(strings.ToLower(e.DeviceType), "Advertised device type: "+e.DeviceType, 3)
		}
		model := strings.ToLower(e.Model)
		if strings.EqualFold(e.Manufacturer, "Sony") && (strings.HasPrefix(model, "kd-") || strings.HasPrefix(model, "xr-") || strings.HasPrefix(model, "kdl-")) {
			add("tv", "Advertised Sony television model: "+e.Model, 3)
		}
		for _, m := range []struct{ prefix, kind string }{{"appletv", "tv"}, {"homepod", "speaker"}, {"audioaccessory", "speaker"}, {"macbook", "laptop"}, {"iphone", "phone"}, {"ipad", "tablet"}, {"imac", "desktop"}} {
			if strings.HasPrefix(model, m.prefix) {
				add(m.kind, "Advertised model: "+e.Model, 3)
			}
		}
		switch strings.TrimSuffix(e.ServiceType, ".") {
		case "_ipp._tcp", "_ipps._tcp", "_printer._tcp", "_pdl-datastream._tcp":
			add("printer", "Advertises printing service "+e.ServiceType, 2)
		case "_wled._tcp":
			add("lighting", "Advertises WLED lighting service", 2)
		case "_home-assistant._tcp":
			add("server", "Advertises Home Assistant service", 2)
		case "_homeconnect._tcp":
			add("appliance", "Advertises Home Connect appliance service", 2)
		}
	}
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	for _, t := range tokens {
		switch t {
		case "printer", "camera", "speaker", "laptop", "desktop", "tablet", "phone", "server", "tv", "appliance", "lighting":
			add(t, "Inferred from device name", 1)
		case "television":
			add("tv", "Inferred from device name", 1)
		case "iphone":
			add("phone", "Inferred from iPhone hostname", 1)
		case "ipad":
			add("tablet", "Inferred from iPad hostname", 1)
		case "macbook", "macbookpro", "macbookair":
			add("laptop", "Inferred from MacBook hostname", 1)
		case "imac":
			add("desktop", "Inferred from iMac hostname", 1)
		case "raspberrypi", "homeassistant":
			add("server", "Inferred from server hostname", 1)
		}
	}
	if conflict {
		return "unknown", "Conflicting device-type evidence", false
	}
	return category, reason, score == 1
}
