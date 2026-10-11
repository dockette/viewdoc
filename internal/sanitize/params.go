package sanitize

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	MaxKeys     = 32
	MaxValueLen = 4096
)

var keyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// Params validates a key/value map intended to be forwarded to a viewer sidecar.
// Returns a defensively-copied map on success.
//
// Keys become VIEWDOC_Q_<KEY> shell variables upper-cased, so keys that only
// differ in case would collide and are rejected. The "url" param (any case)
// must be an absolute http(s) URL: viewdoc.sh hands it to apps as a bare
// argument, where anything else could be parsed as an option.
func Params(in map[string]string) (map[string]string, error) {
	if len(in) > MaxKeys {
		return nil, fmt.Errorf("too many keys: %d (max %d)", len(in), MaxKeys)
	}
	out := make(map[string]string, len(in))
	seen := make(map[string]string, len(in))
	for k, v := range in {
		if !keyRe.MatchString(k) {
			return nil, fmt.Errorf("invalid key %q", k)
		}
		if len(v) > MaxValueLen {
			return nil, fmt.Errorf("value for %q too long: %d (max %d)", k, len(v), MaxValueLen)
		}
		upper := strings.ToUpper(k)
		if prev, dup := seen[upper]; dup {
			return nil, fmt.Errorf("keys %q and %q collide", prev, k)
		}
		seen[upper] = k
		if upper == "URL" {
			if err := checkURL(v); err != nil {
				return nil, fmt.Errorf("invalid %s: %w", k, err)
			}
		}
		out[k] = v
	}
	return out, nil
}

// checkURL accepts only absolute http(s) URLs with a host, written with a
// lower-case scheme (viewdoc.sh matches the literal "http://" / "https://").
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}
