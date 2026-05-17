// gen-cert writes a long-lived self-signed TLS cert to certs/server.{crt,key}.
// SANs are IPs only — viewdoc is accessed by IP:port, no DNS names.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const outDir = "certs"

func main() {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}

	if len(os.Args) > 1 {
		for _, a := range os.Args[1:] {
			ip := net.ParseIP(a)
			if ip == nil {
				log.Fatalf("not an IP: %q", a)
			}
			ips = appendUnique(ips, ip)
		}
	} else {
		for _, ip := range discoverIPs() {
			ips = appendUnique(ips, ip)
		}
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("keygen: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		log.Fatalf("serial: %v", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "viewdoc self-signed"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           ips,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		log.Fatalf("sign: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		log.Fatalf("marshal key: %v", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	crtPath := filepath.Join(outDir, "server.crt")
	keyPath := filepath.Join(outDir, "server.key")
	if err := writePEM(crtPath, "CERTIFICATE", der, 0o644); err != nil {
		log.Fatal(err)
	}
	if err := writePEM(keyPath, "PRIVATE KEY", keyDER, 0o644); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("wrote %s and %s\nIP SANs:\n", crtPath, keyPath)
	for _, ip := range ips {
		fmt.Printf("  %s\n", ip)
	}
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func discoverIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Fatalf("interfaces: %v", err)
	}
	var out []net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLinkLocalMulticast() {
			continue
		}
		out = append(out, ipn.IP)
	}
	return out
}

func appendUnique(s []net.IP, ip net.IP) []net.IP {
	for _, x := range s {
		if x.Equal(ip) {
			return s
		}
	}
	return append(s, ip)
}
