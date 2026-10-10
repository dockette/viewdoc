package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dockette/viewdoc/internal/slots"
)

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func sidecar(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestReadinessRequiresVNCAndSidecar(t *testing.T) {
	vnc := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(vnc.Close)
	vncUp := strings.TrimPrefix(vnc.URL, "http://")
	vncDown := closedAddr(t)

	cases := []struct {
		name         string
		vnc, sidecar string
		wantVNC      bool
		wantSidecar  bool
	}{
		{"both up", vncUp, sidecar(t, http.StatusOK), true, true},
		{"sidecar unhealthy", vncUp, sidecar(t, http.StatusInternalServerError), true, false},
		{"sidecar down", vncUp, closedAddr(t), true, false},
		{"vnc down", vncDown, sidecar(t, http.StatusOK), false, true},
	}

	var entries []string
	for _, c := range cases {
		entries = append(entries, "http://"+c.vnc+"|http://"+c.sidecar)
	}
	pool, err := slots.FromCSV(strings.Join(entries, ","), slots.Defaults{})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	New(pool).Slots(rec, httptest.NewRequest(http.MethodGet, "/api/slots", nil))
	var got struct {
		Slots []slotEntry `json:"slots"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != len(cases) {
		t.Fatalf("got %d slots, want %d", len(got.Slots), len(cases))
	}
	for i, c := range cases {
		e := got.Slots[i]
		if e.VNC != c.wantVNC || e.Sidecar != c.wantSidecar || e.Ready != (c.wantVNC && c.wantSidecar) {
			t.Errorf("%s: vnc=%v sidecar=%v ready=%v, want vnc=%v sidecar=%v ready=%v",
				c.name, e.VNC, e.Sidecar, e.Ready, c.wantVNC, c.wantSidecar, c.wantVNC && c.wantSidecar)
		}
	}
}

func TestHealthzUnavailableWithoutReadySlot(t *testing.T) {
	vnc := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(vnc.Close)
	// VNC is up but the sidecar is not: the slot must not count as ready.
	pool, err := slots.FromCSV("http://"+strings.TrimPrefix(vnc.URL, "http://")+"|http://"+closedAddr(t), slots.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New(pool).Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusServiceUnavailable, rec.Body)
	}
}
