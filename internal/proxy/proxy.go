package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/dockette/viewdoc/internal/slots"
)

// insecureTransport is shared by all https slot proxies. KasmVNC presents a
// self-signed cert on :6901; skip-verify is intentional — the only auth
// surface that matters is the in-image VNC password.
var insecureTransport = &http.Transport{
	TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
	ForceAttemptHTTP2: true,
}

// Both names must be 15 chars: the rewrite is a same-length byte swap inside
// the minified kclient bundle, so source maps and offsets stay aligned.
const (
	secureCtxPropOrig = "isSecureContext"
	secureCtxPropNew  = "_viewdocSecure_"
)

// ForSlot returns a handler that proxies requests under prefix to the slot.
// Prefix is the path the slot is mounted at on the parent listener (e.g.
// "/slot/3") — same origin as the parent page so a user gesture on the
// outer page activates the iframe's AudioContext.
func ForSlot(s slots.Slot) http.Handler {
	prefix := s.PathPrefix()
	target := &url.URL{Scheme: s.VNCScheme, Host: s.VNCAddr()}
	rp := httputil.NewSingleHostReverseProxy(target)
	if target.Scheme == slots.SchemeHTTPS {
		rp.Transport = insecureTransport
	}
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		// Strip prefix so the upstream sees its native paths.
		req.URL.Path = stripPrefix(req.URL.Path, prefix)
		req.URL.RawPath = ""
		origDirector(req)
		req.Host = target.Host
		// Force identity encoding so ModifyResponse can patch the body in place.
		req.Header.Set("Accept-Encoding", "identity")
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		return rewriteResponse(resp, prefix)
	}
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
	}

	// Redirect /slot/{i} → /slot/{i}/ so relative URLs in HTML resolve under
	// the slot's prefix instead of `/slot/`.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix {
			http.Redirect(w, r, prefix+"/"+queryString(r), http.StatusTemporaryRedirect)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func stripPrefix(p, prefix string) string {
	if strings.HasPrefix(p, prefix+"/") {
		return p[len(prefix):]
	}
	if p == prefix {
		return "/"
	}
	return p
}

func queryString(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

// rewriteResponse patches three things on the way back to the browser:
//   - JS: rename `isSecureContext` to `_viewdocSecure_` so kclient's
//     secure-context gate clears over plain http.
//   - HTML: inject a shim that (a) sets `_viewdocSecure_=true` and (b)
//     monkey-patches WebSocket/fetch/XHR to prepend the slot prefix on any
//     same-origin absolute URL the upstream JS constructs at runtime
//     (`/websocket`, `/websockify`, `/api/audio`, …).
func rewriteResponse(resp *http.Response, prefix string) error {
	if resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/javascript"), strings.HasPrefix(ct, "text/javascript"):
		return rewriteBody(resp, func(b []byte) []byte {
			if !bytes.Contains(b, []byte(secureCtxPropOrig)) {
				return b
			}
			return bytes.ReplaceAll(b, []byte(secureCtxPropOrig), []byte(secureCtxPropNew))
		})
	case strings.HasPrefix(ct, "text/html"):
		shim := htmlShim(prefix)
		return rewriteBody(resp, func(b []byte) []byte {
			if i := bytes.Index(b, []byte("<head>")); i >= 0 {
				return splice(b, i+len("<head>"), shim)
			}
			if i := bytes.Index(b, []byte("</body>")); i >= 0 {
				return splice(b, i, shim)
			}
			return b
		})
	}
	return nil
}

// htmlShimTemplate is the inline script injected at the top of every HTML
// response from the upstream. The %s is replaced by the JSON-encoded slot
// prefix (e.g. "/slot/0"); JSON-encoding keeps it safe to embed even if it
// ever contains characters that would otherwise need escaping.
//
// What the shim does, top to bottom:
//
//   1. _viewdocSecure_ = true — kclient (Webtop) gates VideoDecoder access on
//      `isSecureContext`. The body rewriter renames that property in the JS
//      bundle to `_viewdocSecure_`, and we set it true here so the gate clears
//      over plain http.
//
//   2. Seed KasmVNC clipboard settings in localStorage. KasmVNC 1.18 detects
//      iframe embedding (window.self !== window.top) and, unless the URL has
//      ?show_control_bar=…, takes an "embedded" branch that initSettings
//      clipboard_up/down/seamless = false — deliberately disabling host↔guest
//      clipboard sync. initSetting is a no-op when the key already exists, so
//      writing them here wins and leaves the rest of the embedded-mode chrome
//      (hidden control bar, resize=remote) intact. Webtop's kclient uses
//      different, URL-prefixed keys so it's unaffected.
//
//   3. wrap()/WebSocket/fetch/XHR/EventSource patches — rewrite any same-origin
//      absolute URL the upstream JS constructs at runtime (`/websockify`,
//      `/api/audio`, …) to prepend the slot prefix. Comparing URL.host (not
//      origin) is deliberate: ws:// URLs have a different scheme than the
//      page's https://, so origin never matches and KasmVNC's
//      `new WebSocket("wss://host:8443/websockify")` would slip through.
//
//   4. console hook — forwards each iframe's console messages to the parent
//      via postMessage so DevTools on the outer page shows a unified
//      "[slot N] …" stream instead of requiring frame-filter juggling.
const htmlShimTemplate = `<script>
(function () {
  window._viewdocSecure_ = true;
  var P = %s;

  try {
    var LS = window.localStorage;
    ["clipboard_up", "clipboard_down", "clipboard_seamless"].forEach(function (k) {
      if (LS.getItem(k) === null) LS.setItem(k, "true");
    });
  } catch (e) {}

  function wrap(u) {
    if (typeof u !== "string") return u;
    try {
      var x = new URL(u, location.href);
      if (x.host !== location.host) return u;
      if (x.pathname === P || x.pathname.indexOf(P + "/") === 0) return u;
      x.pathname = P + x.pathname;
      return x.toString();
    } catch (e) {
      return u;
    }
  }

  var W = window.WebSocket;
  if (W) {
    window.WebSocket = function (u, p) {
      return p === undefined ? new W(wrap(u)) : new W(wrap(u), p);
    };
    window.WebSocket.prototype = W.prototype;
    window.WebSocket.CONNECTING = W.CONNECTING;
    window.WebSocket.OPEN = W.OPEN;
    window.WebSocket.CLOSING = W.CLOSING;
    window.WebSocket.CLOSED = W.CLOSED;
  }

  var F = window.fetch;
  if (F) {
    window.fetch = function (i, o) {
      if (typeof i === "string") i = wrap(i);
      else if (i && i.url) i = new Request(wrap(i.url), i);
      return F.call(this, i, o);
    };
  }

  var X = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function (m, u) {
    arguments[1] = wrap(u);
    return X.apply(this, arguments);
  };

  var E = window.EventSource;
  if (E) {
    window.EventSource = function (u, o) { return new E(wrap(u), o); };
    window.EventSource.prototype = E.prototype;
  }

  ["log", "info", "warn", "error", "debug"].forEach(function (L) {
    var O = console[L];
    if (!O) return;
    console[L] = function () {
      try {
        var a = [];
        for (var i = 0; i < arguments.length; i++) {
          var v = arguments[i];
          try {
            a.push(typeof v === "string" ? v : (v && v.message) ? v.message : String(v));
          } catch (e) {
            a.push("[unloggable]");
          }
        }
        if (window.parent && window.parent !== window) {
          window.parent.postMessage(
            { _viewdocConsole: true, slot: P, level: L, args: a },
            location.origin
          );
        }
      } catch (e) {}
      return O.apply(console, arguments);
    };
  });

  window.addEventListener("error", function (e) {
    try {
      if (window.parent && window.parent !== window) {
        window.parent.postMessage(
          {
            _viewdocConsole: true,
            slot: P,
            level: "error",
            args: [(e.message || "error") + " @ " + (e.filename || "?") + ":" + (e.lineno || 0)]
          },
          location.origin
        );
      }
    } catch (_) {}
  });
})();
</script>`

func htmlShim(prefix string) string {
	p, _ := jsonString(prefix)
	return fmt.Sprintf(htmlShimTemplate, p)
}

// jsonString produces a JSON-encoded string literal without pulling in encoding/json.
func jsonString(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\', '/':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

func rewriteBody(resp *http.Response, fn func([]byte) []byte) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	out := fn(body)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	if len(out) != len(body) {
		resp.ContentLength = int64(len(out))
		resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	}
	// Upstream validators are for the *unrewritten* bytes; keeping them
	// would let a 304 serve the pre-rewrite body from cache.
	resp.Header.Del("ETag")
	resp.Header.Del("Last-Modified")
	resp.Header.Set("Cache-Control", "no-store")
	return nil
}

func splice(b []byte, at int, s string) []byte {
	out := make([]byte, 0, len(b)+len(s))
	out = append(out, b[:at]...)
	out = append(out, s...)
	out = append(out, b[at:]...)
	return out
}
