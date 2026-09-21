// Package version holds the build version. Override at build time with:
//
//	go build -ldflags "-X labdoc/internal/version.Version=1.2.3" ./cmd/labdoc
package version

import "strings"

var Version = "1.0.0"

// String returns Version without a leading "v", so release tags such as
// "v0.2.0" and plain "0.2.0" display the same.
func String() string { return strings.TrimPrefix(Version, "v") }
