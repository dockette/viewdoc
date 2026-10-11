// Package tlsself provides the control-center's TLS cert, from one of three
// sources (see Options.Mode):
//
//   - embedded: the pre-generated self-signed pair committed under certs/
//     and embedded in the binary (cmd/gen-cert regenerates it). Same
//     fingerprint everywhere, so the browser's "accept once" sticks.
//   - generate: a self-signed pair generated on demand — kept in a
//     directory (a volume in compose) so it survives restarts, or in memory.
//   - file: your own cert + key files, e.g. issued by a real CA.
package tlsself

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Modes accepted by Config.
const (
	ModeEmbedded = "embedded"
	ModeGenerate = "generate"
	ModeFile     = "file"
)

// Options selects and parameterises the cert source.
type Options struct {
	// Mode is ModeEmbedded, ModeGenerate or ModeFile. Empty infers it:
	// file when CertFile is set, generate when Dir is set, else embedded.
	Mode string

	EmbeddedCert, EmbeddedKey []byte // ModeEmbedded

	Dir   string   // ModeGenerate: persist here; empty = in memory
	Hosts []string // ModeGenerate: extra SANs for a new cert

	CertFile, KeyFile string // ModeFile
}

// Config returns the TLS config for o, plus a description of where the cert
// came from (mode, location, SHA-256 fingerprint) for the startup log.
func Config(o Options) (*tls.Config, string, error) {
	mode := o.Mode
	if mode == "" {
		switch {
		case o.CertFile != "" || o.KeyFile != "":
			mode = ModeFile
		case o.Dir != "":
			mode = ModeGenerate
		default:
			mode = ModeEmbedded
		}
	}
	switch mode {
	case ModeEmbedded:
		if len(o.EmbeddedCert) == 0 || len(o.EmbeddedKey) == 0 {
			return nil, "", errors.New("no embedded cert in this build")
		}
		cfg, fp, err := Load(o.EmbeddedCert, o.EmbeddedKey)
		return cfg, "embedded (pre-generated), sha256 " + fp, err
	case ModeGenerate:
		cfg, src, err := LoadOrCreate(o.Dir, o.Hosts)
		return cfg, "generate: " + src, err
	case ModeFile:
		if o.CertFile == "" || o.KeyFile == "" {
			return nil, "", errors.New("file mode needs both TLS_CERT_FILE and TLS_KEY_FILE")
		}
		certPEM, err := os.ReadFile(o.CertFile)
		if err != nil {
			return nil, "", err
		}
		keyPEM, err := os.ReadFile(o.KeyFile)
		if err != nil {
			return nil, "", err
		}
		cfg, fp, err := Load(certPEM, keyPEM)
		return cfg, fmt.Sprintf("file %s, sha256 %s", o.CertFile, fp), err
	}
	return nil, "", fmt.Errorf("unknown TLS_CERT mode %q (want %s, %s or %s)", mode, ModeEmbedded, ModeGenerate, ModeFile)
}

const (
	CertFile = "server.crt"
	KeyFile  = "server.key"
	validity = 10 * 365 * 24 * time.Hour
)

// LoadOrCreate returns a TLS config for dir/server.{crt,key}, generating and
// writing a self-signed pair for hosts (plus localhost) when either file is
// missing. An empty dir generates an in-memory cert that changes on every
// start. The returned string describes where the cert came from.
func LoadOrCreate(dir string, hosts []string) (*tls.Config, string, error) {
	if dir == "" {
		certPEM, keyPEM, err := Generate(hosts)
		if err != nil {
			return nil, "", err
		}
		cfg, fp, err := Load(certPEM, keyPEM)
		return cfg, "generated in memory, changes on every start (set TLS_CERT_DIR to keep it), sha256 " + fp, err
	}

	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	certPEM, cerr := os.ReadFile(certPath)
	keyPEM, kerr := os.ReadFile(keyPath)
	if cerr == nil && kerr == nil {
		cfg, fp, err := Load(certPEM, keyPEM)
		return cfg, fmt.Sprintf("loaded from %s, sha256 %s", dir, fp), err
	}
	for _, err := range []error{cerr, kerr} {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, "", err
		}
	}

	certPEM, keyPEM, err := Generate(hosts)
	if err != nil {
		return nil, "", err
	}
	if err := Write(dir, certPEM, keyPEM); err != nil {
		return nil, "", err
	}
	cfg, fp, err := Load(certPEM, keyPEM)
	return cfg, fmt.Sprintf("generated into %s, sha256 %s", dir, fp), err
}

// Generate creates a self-signed ECDSA P-256 cert valid for ten years. Each
// host is added as an IP or DNS SAN; localhost, 127.0.0.1 and ::1 are always
// included.
func Generate(hosts []string) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("keygen: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "viewdoc self-signed"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range append([]string{"localhost", "127.0.0.1", "::1"}, hosts...) {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = appendUniqueIP(tmpl.IPAddresses, ip)
		} else if h != "" && !slices.Contains(tmpl.DNSNames, h) {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("sign: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// Write stores the pair as dir/server.{crt,key}; the key is readable by the
// owner only.
func Write(dir string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, KeyFile), keyPEM, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, CertFile), certPEM, 0o644)
}

// Load builds a TLS config from a PEM pair and returns the leaf's SHA-256
// fingerprint.
func Load(certPEM, keyPEM []byte) (*tls.Config, string, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("x509 key pair: %w", err)
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, hex.EncodeToString(sum[:]), nil
}

func appendUniqueIP(s []net.IP, ip net.IP) []net.IP {
	for _, x := range s {
		if x.Equal(ip) {
			return s
		}
	}
	return append(s, ip)
}
