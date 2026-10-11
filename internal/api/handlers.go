package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockette/viewdoc/internal/sanitize"
	"github.com/dockette/viewdoc/internal/slots"
	"github.com/dockette/viewdoc/internal/version"
)

type Handlers struct {
	Pool   *slots.Pool
	Client *http.Client
	// Probe is used for the sidecar /healthz check; short timeout so one
	// hung sidecar can't stall /api/slots, which the UI polls every 5s.
	Probe *http.Client
}

func New(pool *slots.Pool) *Handlers {
	return &Handlers{
		Pool:   pool,
		Client: &http.Client{Timeout: 3 * time.Second},
		Probe:  &http.Client{Timeout: time.Second},
	}
}

type slotEntry struct {
	Index   int    `json:"idx"`
	Host    string `json:"host"`
	IP      string `json:"ip,omitempty"`
	Path    string `json:"path"`
	VNC     bool   `json:"vnc"`
	Sidecar bool   `json:"sidecar"`
	Ready   bool   `json:"ready"`
}

// readiness probes every slot in parallel. IP is best-effort (empty on
// resolution failure); Path is the same-origin prefix the browser uses to
// build the iframe URL. A slot is Ready only when both the VNC port accepts
// TCP and the sidecar answers /healthz — otherwise the desktop shows up but
// opening a file fails with 502.
func (h *Handlers) readiness() (ready int, entries []slotEntry) {
	all := h.Pool.All()
	entries = make([]slotEntry, len(all))
	var wg sync.WaitGroup
	for i, s := range all {
		wg.Add(1)
		go func(i int, s slots.Slot) {
			defer wg.Done()
			vnc, sidecar := probe(s.VNCAddr()), h.probeSidecar(s)
			entries[i] = slotEntry{
				Index:   s.Index,
				Host:    s.Host,
				IP:      resolveIP(s.Host),
				Path:    s.PathPrefix() + "/",
				VNC:     vnc,
				Sidecar: sidecar,
				Ready:   vnc && sidecar,
			}
		}(i, s)
	}
	wg.Wait()
	for _, e := range entries {
		if e.Ready {
			ready++
		}
	}
	return
}

func resolveIP(host string) string {
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0]
}

func (h *Handlers) Slots(w http.ResponseWriter, _ *http.Request) {
	_, entries := h.readiness()
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(entries),
		"slots": entries,
	})
}

func (h *Handlers) Healthz(w http.ResponseWriter, _ *http.Request) {
	ready, entries := h.readiness()
	status := http.StatusOK
	if ready == 0 {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ready": ready, "total": len(entries), "version": version.Version})
}

func (h *Handlers) Params(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	slot, ok := h.Pool.Get(idx)
	if !ok {
		http.Error(w, "slot not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var in struct {
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	clean, err := sanitize.Params(in.Params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("params slot=%d host=%s %s", idx, slot.Host, paramsForLog(clean))
	payload, _ := json.Marshal(map[string]any{"params": clean})
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, slot.SidecarURL()+"/params", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(req)
	if err != nil {
		log.Printf("params slot=%d sidecar unreachable: %v", idx, err)
		http.Error(w, fmt.Sprintf("sidecar unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// Restart asks the slot's sidecar to exit with its restart code; the viewer
// image turns that into a container exit and the orchestrator (Nomad, or
// compose's restart policy) brings up a fresh one. Requiring a JSON
// Content-Type forces a CORS preflight, so a cross-site form can't trigger it.
func (h *Handlers) Restart(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	slot, ok := h.Pool.Get(idx)
	if !ok {
		http.Error(w, "slot not found", http.StatusNotFound)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	log.Printf("restart slot=%d host=%s", idx, slot.Host)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, slot.SidecarURL()+"/restart", nil)
	resp, err := h.Client.Do(req)
	if err != nil {
		log.Printf("restart slot=%d sidecar unreachable: %v", idx, err)
		http.Error(w, fmt.Sprintf("sidecar unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// Status relays the slot sidecar's GET /status (latest request's phase, app,
// error, and whether the launched app is still running).
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	slot, ok := h.Pool.Get(idx)
	if !ok {
		http.Error(w, "slot not found", http.StatusNotFound)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, slot.SidecarURL()+"/status", nil)
	resp, err := h.Probe.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("sidecar unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// paramsForLog renders the sanitized params as space-separated k=v pairs,
// sorted by key so identical requests log identically, and truncates long
// values so log lines stay readable.
func paramsForLog(p map[string]string) string {
	if len(p) == 0 {
		return "(empty)"
	}
	pairs := make([]string, 0, len(p))
	for _, k := range slices.Sorted(maps.Keys(p)) {
		v := p[k]
		if len(v) > 120 {
			v = v[:117] + "..."
		}
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, v))
	}
	return strings.Join(pairs, " ")
}

func probe(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (h *Handlers) probeSidecar(s slots.Slot) bool {
	resp, err := h.Probe.Get(s.SidecarURL() + "/healthz")
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
