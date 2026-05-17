package slots

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"

	// Matches Kasm; Webtop entries must spell out :3000.
	DefaultVNCPort = 6901
	SidecarPort    = 7000
)

// Slot is a single viewer backend reachable on the compose network.
// KasmVNC serves HTTPS on :6901 even when require_ssl is false, so the
// proxy speaks HTTPS to those slots with InsecureSkipVerify.
type Slot struct {
	Index     int
	Host      string
	VNCPort   int
	VNCScheme string
}

func (s Slot) VNCAddr() string    { return fmt.Sprintf("%s:%d", s.Host, s.VNCPort) }
func (s Slot) VNCURL() string     { return fmt.Sprintf("%s://%s:%d", s.VNCScheme, s.Host, s.VNCPort) }
func (s Slot) SidecarURL() string { return fmt.Sprintf("http://%s:%d/params", s.Host, SidecarPort) }

// PathPrefix is the URL prefix the slot is mounted under on the control-center
// listener, without trailing slash, e.g. "/slot/3".
func (s Slot) PathPrefix() string { return fmt.Sprintf("/slot/%d", s.Index) }

type Pool struct{ slots []Slot }

// FromCSV parses comma-separated entries. Accepted forms:
//
//	host                           -> http://host:6901
//	host:port                      -> http://host:port
//	scheme://host[:port]           -> scheme://host:(port or 6901)
func FromCSV(csv string) (*Pool, error) {
	parts := strings.Split(csv, ",")
	out := make([]Slot, 0, len(parts))
	for _, p := range parts {
		raw := strings.TrimSpace(p)
		if raw == "" {
			continue
		}
		slot, err := parseEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("VIEWER_SLOTS[%d] %q: %w", len(out), raw, err)
		}
		slot.Index = len(out)
		out = append(out, slot)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no slots configured")
	}
	return &Pool{slots: out}, nil
}

func parseEntry(raw string) (Slot, error) {
	scheme := SchemeHTTP
	rest := raw
	if i := strings.Index(raw, "://"); i > 0 {
		scheme = raw[:i]
		if scheme != SchemeHTTP && scheme != SchemeHTTPS {
			return Slot{}, fmt.Errorf("unsupported scheme %q", scheme)
		}
		rest = raw[i+3:]
	}
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || u.Host == "" {
		return Slot{}, fmt.Errorf("cannot parse")
	}
	port := DefaultVNCPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return Slot{}, fmt.Errorf("invalid port %q", p)
		}
		port = n
	}
	return Slot{Host: u.Hostname(), VNCPort: port, VNCScheme: scheme}, nil
}

func (p *Pool) All() []Slot { return p.slots }
func (p *Pool) Len() int    { return len(p.slots) }
func (p *Pool) Get(i int) (Slot, bool) {
	if i < 0 || i >= len(p.slots) {
		return Slot{}, false
	}
	return p.slots[i], true
}
