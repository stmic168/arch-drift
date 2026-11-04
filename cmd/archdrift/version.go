package main

import (
	"regexp"
	"runtime/debug"
)

// version is set at release build time with -ldflags "-X main.version=vX.Y.Z".
var version string

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// toolVersion is the release tag of this binary: the -ldflags value, else the
// module version recorded by `go install ...@vX.Y.Z`, else "dev".
func toolVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// actionRef is the ref init pins the GitHub Action to: the release tag of this
// binary, or main for development builds and pseudo-versions.
func actionRef(v string) string {
	if releaseTag.MatchString(v) {
		return v
	}
	return "main"
}
