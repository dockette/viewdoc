package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/dockette/viewdoc/internal/slots"
)

// upstreamSlot starts an upstream that records the last request it saw and
// returns a slot pointing at it.
func upstreamSlot(t *testing.T, h http.HandlerFunc) slots.Slot {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	host, port, err := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return slots.Slot{Index: 3, Host: host, VNCPort: p, VNCScheme: slots.SchemeHTTP}
}

func TestForSlotRewritesRequest(t *testing.T) {
	var got *http.Request
	s := upstreamSlot(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png")
	})

	req := httptest.NewRequest(http.MethodGet, "http://cc.example/slot/3/vnc/app.png?x=1", nil)
	req.RemoteAddr = "10.0.0.9:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	ForSlot(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "png" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	checks := []struct{ name, got, want string }{
		{"path", got.URL.Path, "/vnc/app.png"},
		{"query", got.URL.RawQuery, "x=1"},
		{"host", got.Host, s.VNCAddr()},
		{"accept-encoding", got.Header.Get("Accept-Encoding"), "identity"},
		{"x-forwarded-for", got.Header.Get("X-Forwarded-For"), "203.0.113.7, 10.0.0.9"},
		{"x-forwarded-host", got.Header.Get("X-Forwarded-Host"), ""},
		{"x-forwarded-proto", got.Header.Get("X-Forwarded-Proto"), ""},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestForSlotRedirectsBarePrefix(t *testing.T) {
	s := upstreamSlot(t, func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be hit")
	})
	rec := httptest.NewRecorder()
	ForSlot(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slot/3?resize=remote", nil))
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/slot/3/?resize=remote" {
		t.Fatalf("got %d Location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestForSlotRewritesBodies(t *testing.T) {
	s := upstreamSlot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<html><head><title>x</title></head></html>")
		case "/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = io.WriteString(w, "if (window.isSecureContext) go();")
		}
	})
	h := ForSlot(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slot/3/", nil))
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<html><head><script>") || !strings.Contains(body, `var P = "\/slot\/3";`) {
		t.Errorf("HTML shim not injected after <head>: %.120q", body)
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length = %q, body is %d bytes", rec.Header().Get("Content-Length"), len(body))
	}
	if rec.Header().Get("ETag") != "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("cache headers: ETag=%q Cache-Control=%q", rec.Header().Get("ETag"), rec.Header().Get("Cache-Control"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slot/3/app.js", nil))
	if got, want := rec.Body.String(), "if (window._viewdocSecure_) go();"; got != want {
		t.Errorf("JS = %q, want %q", got, want)
	}
}
