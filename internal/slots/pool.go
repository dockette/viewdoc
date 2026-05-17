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

	// "kasm" and "webtop" are pseudo-schemes accepted in VIEWER_SLOTS entries.
	// They expand to a real scheme + default port pair sourced from Defaults,
	// so an operator can write `kasm://node-1` or `webtop://node-1` instead of
	// remembering the canonical port/scheme.
	SchemeKasm   = "kasm"
	SchemeWebtop = "webtop"

	// Hardcoded fallbacks if the operator does not override via env.
	// Kasm serves NoVNC over HTTPS on 6901; Webtop over HTTP on 3000;
	// the sidecar listens on 7000.
	DefaultKasmPort    = 6901
	DefaultWebtopPort  = 3000
	DefaultSidecarPort = 7000
)

// Defaults are the per-slot fallbacks applied when an entry in VIEWER_SLOTS
// omits a port. They are normally sourced from environment variables in
// cmd/control-center (VIEWER_KASM_DEFAULT_PORT, VIEWER_WEBTOP_DEFAULT_PORT,
// VIEWER_SIDECAR_DEFAULT_PORT).
//
//	KasmPort    — port for bare entries and `kasm://` pseudo-scheme (https)
//	WebtopPort  — port for the `webtop://` pseudo-scheme (http)
//	SidecarPort — port for the sidecar half when omitted
type Defaults struct {
	KasmPort    int
	WebtopPort  int
	SidecarPort int
}

// Slot is a single viewer backend reachable on the compose / Nomad network.
// KasmVNC serves HTTPS on :6901 even when require_ssl is false, so the
// proxy speaks HTTPS to those slots with InsecureSkipVerify.
//
// The sidecar endpoint is tracked separately so deployments that publish
// NoVNC and the sidecar on different host:port pairs (e.g. Nomad dynamic
// ports) can declare both.
type Slot struct {
	Index int

	Host      string
	VNCPort   int
	VNCScheme string

	SidecarHost   string
	SidecarPort   int
	SidecarScheme string
}

func (s Slot) VNCAddr() string { return fmt.Sprintf("%s:%d", s.Host, s.VNCPort) }
func (s Slot) VNCURL() string  { return fmt.Sprintf("%s://%s:%d", s.VNCScheme, s.Host, s.VNCPort) }

func (s Slot) SidecarAddr() string {
	return fmt.Sprintf("%s:%d", s.SidecarHost, s.SidecarPort)
}
func (s Slot) SidecarURL() string {
	return fmt.Sprintf("%s://%s:%d/params", s.SidecarScheme, s.SidecarHost, s.SidecarPort)
}

// PathPrefix is the URL prefix the slot is mounted under on the control-center
// listener, without trailing slash, e.g. "/slot/3".
func (s Slot) PathPrefix() string { return fmt.Sprintf("/slot/%d", s.Index) }

type Pool struct{ slots []Slot }

// FromCSV parses comma-separated entries. Each entry has the shape
//
//	<vnc>[|<sidecar>]
//
// where the vnc half accepts:
//
//	host                  -> http://host:<KasmPort>      (bare = http + kasm port)
//	host:port             -> http://host:port
//	kasm://host[:port]    -> https://host:(port or KasmPort)    (kasm needs TLS)
//	webtop://host[:port]  -> http://host:(port or WebtopPort)
//	http://host[:port]    -> http://host:(port or KasmPort)
//	https://host[:port]   -> https://host:(port or KasmPort)
//
// and the sidecar half accepts only http/https/no-scheme:
//
//	host                  -> http://host:<SidecarPort>
//	host:port             -> http://host:port
//	http(s)://host[:port] -> as written, port falls back to SidecarPort
//
// If the sidecar half is omitted, the sidecar host is taken from the vnc
// half and the port from d.SidecarPort.
func FromCSV(csv string, d Defaults) (*Pool, error) {
	if d.KasmPort == 0 {
		d.KasmPort = DefaultKasmPort
	}
	if d.WebtopPort == 0 {
		d.WebtopPort = DefaultWebtopPort
	}
	if d.SidecarPort == 0 {
		d.SidecarPort = DefaultSidecarPort
	}

	parts := strings.Split(csv, ",")
	out := make([]Slot, 0, len(parts))
	for _, p := range parts {
		raw := strings.TrimSpace(p)
		if raw == "" {
			continue
		}
		slot, err := parseEntry(raw, d)
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

func parseEntry(raw string, d Defaults) (Slot, error) {
	vncRaw, sideRaw, hasSide := strings.Cut(raw, "|")
	vncRaw = strings.TrimSpace(vncRaw)
	if vncRaw == "" {
		return Slot{}, fmt.Errorf("empty vnc endpoint")
	}

	vncHost, vncPort, vncScheme, err := parseVNC(vncRaw, d)
	if err != nil {
		return Slot{}, fmt.Errorf("vnc: %w", err)
	}

	sideHost, sidePort, sideScheme := vncHost, d.SidecarPort, SchemeHTTP
	if hasSide {
		sideRaw = strings.TrimSpace(sideRaw)
		if sideRaw == "" {
			return Slot{}, fmt.Errorf("empty sidecar endpoint after %q", "|")
		}
		sideHost, sidePort, sideScheme, err = parseHTTPEndpoint(sideRaw, d.SidecarPort)
		if err != nil {
			return Slot{}, fmt.Errorf("sidecar: %w", err)
		}
	}

	return Slot{
		Host:          vncHost,
		VNCPort:       vncPort,
		VNCScheme:     vncScheme,
		SidecarHost:   sideHost,
		SidecarPort:   sidePort,
		SidecarScheme: sideScheme,
	}, nil
}

// parseVNC understands the kasm:// and webtop:// pseudo-schemes in addition
// to the plain http/https forms.
func parseVNC(raw string, d Defaults) (host string, port int, scheme string, err error) {
	if i := strings.Index(raw, "://"); i > 0 {
		switch raw[:i] {
		case SchemeKasm:
			return parseHostPort(raw[i+3:], d.KasmPort, SchemeHTTPS)
		case SchemeWebtop:
			return parseHostPort(raw[i+3:], d.WebtopPort, SchemeHTTP)
		}
	}
	return parseHTTPEndpoint(raw, d.KasmPort)
}

// parseHTTPEndpoint accepts only http/https schemes (or no scheme, defaulting
// to http). It is used for both the sidecar half and the explicit http(s)://
// form of the vnc half.
func parseHTTPEndpoint(raw string, defaultPort int) (host string, port int, scheme string, err error) {
	scheme = SchemeHTTP
	rest := raw
	if i := strings.Index(raw, "://"); i > 0 {
		scheme = raw[:i]
		if scheme != SchemeHTTP && scheme != SchemeHTTPS {
			return "", 0, "", fmt.Errorf("unsupported scheme %q", scheme)
		}
		rest = raw[i+3:]
	}
	return parseHostPort(rest, defaultPort, scheme)
}

func parseHostPort(rest string, defaultPort int, scheme string) (host string, port int, outScheme string, err error) {
	u, perr := url.Parse(scheme + "://" + rest)
	if perr != nil || u.Host == "" {
		return "", 0, "", fmt.Errorf("cannot parse %q", rest)
	}
	port = defaultPort
	if p := u.Port(); p != "" {
		n, cerr := strconv.Atoi(p)
		if cerr != nil || n <= 0 || n > 65535 {
			return "", 0, "", fmt.Errorf("invalid port %q", p)
		}
		port = n
	}
	return u.Hostname(), port, scheme, nil
}

func (p *Pool) All() []Slot { return p.slots }
func (p *Pool) Len() int    { return len(p.slots) }
func (p *Pool) Get(i int) (Slot, bool) {
	if i < 0 || i >= len(p.slots) {
		return Slot{}, false
	}
	return p.slots[i], true
}
