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
	"strings"
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

func main() {
	addr := envOr("LISTEN_ADDR", ":7000")

	// viewdoc.sh skips a URL equal to the last one it opened. Compose's
	// restart policy reuses the container (and its /tmp), so a fresh
	// desktop would otherwise refuse to reopen the pre-restart URL.
	if err := os.Remove(lastPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("clear %s: %v", lastPath, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("POST /params", handleParams)
	mux.HandleFunc("POST /restart", handleRestart)

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
