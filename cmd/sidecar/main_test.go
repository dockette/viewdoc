package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func useTempStatus(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	oldStatus, oldState := statusPath, statePath
	statusPath, statePath = filepath.Join(dir, "status"), filepath.Join(dir, "state")
	t.Cleanup(func() { statusPath, statePath = oldStatus, oldState })
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getStatus(t *testing.T) status {
	t.Helper()
	rec := httptest.NewRecorder()
	handleStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status code %d", rec.Code)
	}
	var st status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStatusIdleWithoutFile(t *testing.T) {
	useTempStatus(t)
	if st := getStatus(t); st.Phase != "idle" {
		t.Fatalf("phase = %q, want idle", st.Phase)
	}
}

func TestStatusQueuedFromWriteStatus(t *testing.T) {
	useTempStatus(t)
	if err := writeStatus("queued", "https://x/a.pdf\nevil=1"); err != nil {
		t.Fatal(err)
	}
	st := getStatus(t)
	if st.Phase != "queued" || st.URL != "https://x/a.pdf evil=1" || st.Updated == "" {
		t.Fatalf("got %+v", st)
	}
	if st.AppState != "" {
		t.Fatalf("queued must not report app state, got %q", st.AppState)
	}
}

func TestStatusFailedCarriesError(t *testing.T) {
	useTempStatus(t)
	write(t, statusPath, "phase=failed\nurl=https://x/a.zip\napp=xarchiver\nerror=download failed\nupdated=2026-10-11T08:00:00Z\n")
	st := getStatus(t)
	if st.Phase != "failed" || st.App != "xarchiver" || st.Error != "download failed" {
		t.Fatalf("got %+v", st)
	}
}

func TestStatusAppState(t *testing.T) {
	self := strconv.Itoa(os.Getpid())
	cases := []struct {
		name, state string
		want        string
		wantCode    int // -1 = no exit code
	}{
		{"running and alive", self + " running\n", "running", -1},
		{"running but gone", "999999999 running\n", "exited", -1},
		{"handoff", self + " handoff 0\n", "handoff", 0},
		{"exited with code", self + " exited 127\n", "exited", 127},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useTempStatus(t)
			write(t, statusPath, "phase=launched\nurl=https://x/a.mp4\napp=vlc\nerror=\nupdated=2026-10-11T08:00:00Z\n")
			write(t, statePath, c.state)
			st := getStatus(t)
			if st.AppState != c.want {
				t.Fatalf("app_state = %q, want %q", st.AppState, c.want)
			}
			switch {
			case c.wantCode < 0 && st.ExitCode != nil:
				t.Fatalf("exit_code = %d, want none", *st.ExitCode)
			case c.wantCode >= 0 && (st.ExitCode == nil || *st.ExitCode != c.wantCode):
				t.Fatalf("exit_code = %v, want %d", st.ExitCode, c.wantCode)
			}
		})
	}
}
