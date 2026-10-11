package sanitize

import (
	"strings"
	"testing"
)

func TestParamsURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://example.com/x.pdf", true},
		{"http://example.com:8080/a.mp4?x=1#y", true},
		{"https://example.com/a b.pdf", true},
		{"--version.mp4", false},
		{"-x", false},
		{"file:///etc/passwd", false},
		{"ftp://example.com/x.zip", false},
		{"HTTPS://example.com/x.pdf", false},
		{"https:///x.pdf", false},
		{"https:example.com/x.pdf", false},
		{"//example.com/x.pdf", false},
		{"https://example.com/x\n.pdf", false},
		{"", false},
	}
	for _, c := range cases {
		_, err := Params(map[string]string{"url": c.url})
		if (err == nil) != c.ok {
			t.Errorf("url %q: err=%v, want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestParamsURLKeyAnyCase(t *testing.T) {
	for _, k := range []string{"URL", "Url"} {
		if _, err := Params(map[string]string{k: "--version.mp4"}); err == nil {
			t.Errorf("key %q: expected URL check to apply", k)
		}
	}
}

func TestParamsKeyCollision(t *testing.T) {
	_, err := Params(map[string]string{"url": "https://a/x.pdf", "URL": "https://b/y.pdf"})
	if err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("err = %v, want collision", err)
	}
}

func TestParamsKeysAndLimits(t *testing.T) {
	if _, err := Params(map[string]string{"bad-key": "x"}); err == nil {
		t.Error("bad-key accepted")
	}
	if _, err := Params(map[string]string{"note": strings.Repeat("x", MaxValueLen+1)}); err == nil {
		t.Error("oversized value accepted")
	}
	many := map[string]string{}
	for i := range MaxKeys + 1 {
		many["k"+strings.Repeat("a", i)] = "v"
	}
	if _, err := Params(many); err == nil {
		t.Error("too many keys accepted")
	}
	out, err := Params(map[string]string{"note": "free text, not a URL"})
	if err != nil || out["note"] != "free text, not a URL" {
		t.Errorf("plain param: out=%v err=%v", out, err)
	}
}
