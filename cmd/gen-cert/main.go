// gen-cert writes a self-signed TLS cert. By default it regenerates the
// pre-generated pair under certs/ that the control-center embeds
// (TLS_CERT=embedded); -dir writes elsewhere, e.g. into a TLS_CERT_DIR.
//
//	go run ./cmd/gen-cert [-dir certs] [host ...]
//
// Hosts are IPs or DNS names added as SANs (localhost, 127.0.0.1 and ::1 are
// always included). Without hosts, this machine's non-loopback IPs are used.
package main

import (
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net"
	"path/filepath"

	"github.com/dockette/viewdoc/internal/tlsself"
)

func main() {
	dir := flag.String("dir", "certs", "output directory for server.{crt,key}")
	flag.Parse()

	hosts := flag.Args()
	if len(hosts) == 0 {
		hosts = discoverIPs()
	}
	certPEM, keyPEM, err := tlsself.Generate(hosts)
	if err != nil {
		log.Fatal(err)
	}
	if err := tlsself.Write(*dir, certPEM, keyPEM); err != nil {
		log.Fatal(err)
	}

	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s and %s\nSANs:\n", filepath.Join(*dir, tlsself.CertFile), filepath.Join(*dir, tlsself.KeyFile))
	for _, n := range cert.DNSNames {
		fmt.Printf("  %s\n", n)
	}
	for _, ip := range cert.IPAddresses {
		fmt.Printf("  %s\n", ip)
	}
}

func discoverIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Fatalf("interfaces: %v", err)
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLinkLocalMulticast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	return out
}
