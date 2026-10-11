// Sidecar: HTTP listener baked into each viewer image.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dockette/viewdoc/internal/sanitize"
)

const (
	queryDir  = "/tmp"
	jsonName  = "viewdoc.json"
	envName   = "viewdoc.env"
	envPrefix = "VIEWDOC_Q_"
	hookPath  = "/usr/local/bin/viewdoc.sh"
	lastPath  = "/tmp/.viewdoc_last_url"

	// restartExitCode is the sentinel the viewer image's supervisor wrapper
	// (Kasm custom_startup.sh / Webtop s6 finish) turns into a container
	// exit. Any other exit just restarts the sidecar in place.
	restartExitCode = 42
)

// Shared with viewdoc.sh. Vars so tests can point them at a temp dir.
var (
	// statusPath holds key=value lines describing the latest request:
	// phase (queued|refused|deduped|downloading|failed|launched), url, app,
	// error, updated.
	statusPath = "/tmp/viewdoc.status"
	// statePath is "<pid> <running|handoff|exited> [rc]" for the last app
	// viewdoc.sh launched.
	statePath = "/tmp/.viewdoc_last_state"
)

func main() {
	addr := envOr("LISTEN_ADDR", ":7000")

	// viewdoc.sh skips a URL equal to the last one it opened. Compose's
	// restart policy reuses the container (and its /tmp), so a fresh
	// desktop would otherwise refuse to reopen the pre-restart URL.
	// The status of a pre-restart request would be stale for the same reason.
	for _, p := range []string{lastPath, statusPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("clear %s: %v", p, err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("POST /params", handleParams)
	mux.HandleFunc("POST /restart", handleRestart)
	mux.HandleFunc("GET /status", handleStatus)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("sidecar listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func handleParams(w http.ResponseWriter, r *http.Request) {
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

	if err := writeQuery(clean); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Mark the request as queued right away so /status never shows the
	// previous request's outcome while viewdoc.sh is still starting.
	if err := writeStatus("queued", clean["url"]); err != nil {
		log.Printf("write status: %v", err)
	}
	go fireHook()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "keys": len(clean)})
}

// handleRestart replies first, then exits with restartExitCode so the
// control-center sees 202 instead of a dropped connection.
func handleRestart(w http.ResponseWriter, _ *http.Request) {
	log.Printf("restart requested, exiting with code %d", restartExitCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(restartExitCode)
	}()
}

// handleStatus reports what happened to the latest request, plus whether the
// app it launched is still up. Phase "idle" means no request since start.
func handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(readStatus())
}

type status struct {
	Phase    string `json:"phase"`
	URL      string `json:"url,omitempty"`
	App      string `json:"app,omitempty"`
	Error    string `json:"error,omitempty"`
	Updated  string `json:"updated,omitempty"`
	AppState string `json:"app_state,omitempty"` // running|handoff|exited
	ExitCode *int   `json:"exit_code,omitempty"`
}

func readStatus() status {
	b, err := os.ReadFile(statusPath)
	if err != nil {
		return status{Phase: "idle"}
	}
	var st status
	for line := range strings.Lines(string(b)) {
		k, v, _ := strings.Cut(strings.TrimRight(line, "\n"), "=")
		switch k {
		case "phase":
			st.Phase = v
		case "url":
			st.URL = v
		case "app":
			st.App = v
		case "error":
			st.Error = v
		case "updated":
			st.Updated = v
		}
	}
	// Only these phases refer to the app recorded in statePath.
	if st.Phase == "launched" || st.Phase == "deduped" {
		st.AppState, st.ExitCode = readAppState()
	}
	return st
}

// readAppState interprets statePath. A "running" pid that no longer exists
// (killed, or a container restart reused /tmp) is reported as exited.
func readAppState() (string, *int) {
	b, err := os.ReadFile(statePath)
	if err != nil {
		return "", nil
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return "", nil
	}
	var code *int
	if len(f) > 2 {
		if n, err := strconv.Atoi(f[2]); err == nil {
			code = &n
		}
	}
	if f[1] == "running" {
		pid, err := strconv.Atoi(f[0])
		if err != nil || !alive(pid) {
			return "exited", code
		}
	}
	return f[1], code
}

// alive reports whether pid exists; EPERM means it does, owned by another user.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func writeStatus(phase, url string) error {
	url = strings.ReplaceAll(url, "\n", " ")
	return atomicWrite(statusPath, fmt.Appendf(nil, "phase=%s\nurl=%s\napp=\nerror=\nupdated=%s\n",
		phase, url, time.Now().UTC().Format(time.RFC3339)))
}

func writeQuery(m map[string]string) error {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(queryDir, jsonName), append(jsonBytes, '\n')); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(&b, "%s%s=%s\n", envPrefix, strings.ToUpper(k), shellQuote(m[k]))
	}
	if err := atomicWrite(filepath.Join(queryDir, envName), []byte(b.String())); err != nil {
		return fmt.Errorf("write env: %w", err)
	}
	return nil
}

// shellQuote single-quotes a value for safe `source <file>` consumption.
// strconv.Quote produces Go syntax, not shell — there is no stdlib equivalent.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fireHook() {
	cmd := exec.Command(hookPath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
			return
		}
		log.Printf("hook: %v", err)
	}
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}
