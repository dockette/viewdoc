// Package version holds the viewdoc release, a date in YYYY.MM.DD form
// (YYYY.MM.DD.N for a second release on the same day) taken from the VERSION
// file. Builds set it with:
//
//	go build -ldflags "-X github.com/dockette/viewdoc/internal/version.Version=$(cat VERSION)"
package version

// Version is "dev" for builds that don't pass the ldflag.
var Version = "dev"
