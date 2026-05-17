// Package tlsself loads a pre-generated self-signed TLS cert from PEM bytes.
// The cert is built once by cmd/gen-cert and committed under certs/. Embedding
// the PEM into the binary keeps the fingerprint stable across restarts so the
// browser's "accept once" actually sticks.
package tlsself

import (
	"crypto/tls"
	"fmt"
)

func Load(certPEM, keyPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("x509 key pair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
