// Package viewdoc exposes embedded static assets for the control-center.
package viewdoc

import "embed"

//go:embed web
var WebFS embed.FS

//go:embed certs/server.crt
var ServerCert []byte

//go:embed certs/server.key
var ServerKey []byte
