package tlsself

import (
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func leaf(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	cfg, _, err := LoadOrCreate(dir, []string{"10.1.2.3", "viewdoc.example"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadOrCreatePersists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	first := leaf(t, dir)

	if st, err := os.Stat(filepath.Join(dir, KeyFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v mode %v", err, st.Mode())
	}
	if second := leaf(t, dir); !first.Equal(second) {
		t.Fatal("second start generated a new cert instead of loading the stored one")
	}
}

func TestSANs(t *testing.T) {
	c := leaf(t, "")
	for _, n := range []string{"localhost", "viewdoc.example"} {
		if !slices.Contains(c.DNSNames, n) {
			t.Errorf("missing DNS SAN %q in %v", n, c.DNSNames)
		}
	}
	for _, s := range []string{"127.0.0.1", "::1", "10.1.2.3"} {
		ip := net.ParseIP(s)
		if !slices.ContainsFunc(c.IPAddresses, ip.Equal) {
			t.Errorf("missing IP SAN %s in %v", s, c.IPAddresses)
		}
	}
}

func TestInMemoryChangesEachTime(t *testing.T) {
	if leaf(t, "").Equal(leaf(t, "")) {
		t.Fatal("in-memory certs should differ between starts")
	}
}

func TestConfigModes(t *testing.T) {
	embCert, embKey, err := Generate(nil)
	if err != nil {
		t.Fatal(err)
	}
	ownDir := t.TempDir()
	ownCert, ownKey, err := Generate([]string{"own.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(ownDir, ownCert, ownKey); err != nil {
		t.Fatal(err)
	}
	ownCertFile, ownKeyFile := filepath.Join(ownDir, CertFile), filepath.Join(ownDir, KeyFile)
	genDir := filepath.Join(t.TempDir(), "gen")

	fingerprint := func(certPEM, keyPEM []byte) string {
		_, fp, err := Load(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	embFP, ownFP := fingerprint(embCert, embKey), fingerprint(ownCert, ownKey)

	cases := []struct {
		name    string
		o       Options
		wantFP  string // "" = any (freshly generated)
		wantErr bool
	}{
		{"default is embedded", Options{}, embFP, false},
		{"explicit embedded wins over dir", Options{Mode: ModeEmbedded, Dir: genDir}, embFP, false},
		{"dir infers generate", Options{Dir: genDir}, "", false},
		{"generate in memory", Options{Mode: ModeGenerate}, "", false},
		{"cert file infers file", Options{CertFile: ownCertFile, KeyFile: ownKeyFile}, ownFP, false},
		{"file without key", Options{Mode: ModeFile, CertFile: ownCertFile}, "", true},
		{"file missing", Options{CertFile: "/nonexistent.crt", KeyFile: "/nonexistent.key"}, "", true},
		{"unknown mode", Options{Mode: "acme"}, "", true},
		{"embedded but none built in", Options{Mode: ModeEmbedded, EmbeddedCert: []byte{}}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := c.o
			if o.EmbeddedCert == nil {
				o.EmbeddedCert, o.EmbeddedKey = embCert, embKey
			}
			cfg, src, err := Config(o)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got cert from %s", src)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.wantFP != "" && !strings.Contains(src, c.wantFP) {
				t.Fatalf("src %q, want fingerprint %s", src, c.wantFP)
			}
			if len(cfg.Certificates) != 1 {
				t.Fatal("no certificate in config")
			}
		})
	}
}
