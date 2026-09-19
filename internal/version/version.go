// Package version holds the build version. Override at build time with:
//
//	go build -ldflags "-X labdoc/internal/version.Version=1.2.3" ./cmd/labdoc
package version

var Version = "0.1.0"
