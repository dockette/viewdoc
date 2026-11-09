// Package version holds the viewdoc release, a date in YYYY.MM.DD form
// taken from the VERSION
// file. Builds set it with:
//
//	go build -ldflags "-X github.com/dockette/viewdoc/internal/version.Version=$(sed -n s/^VERSION=//p VERSION)"
package version

// Version is "dev" for builds that don't pass the ldflag.
var Version = "dev"
