package sanitize

import (
	"fmt"
	"regexp"
)

const (
	MaxKeys     = 32
	MaxValueLen = 4096
)

var keyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// Params validates a key/value map intended to be forwarded to a viewer sidecar.
// Returns a defensively-copied map on success.
func Params(in map[string]string) (map[string]string, error) {
	if len(in) > MaxKeys {
		return nil, fmt.Errorf("too many keys: %d (max %d)", len(in), MaxKeys)
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !keyRe.MatchString(k) {
			return nil, fmt.Errorf("invalid key %q", k)
		}
		if len(v) > MaxValueLen {
			return nil, fmt.Errorf("value for %q too long: %d (max %d)", k, len(v), MaxValueLen)
		}
		out[k] = v
	}
	return out, nil
}
