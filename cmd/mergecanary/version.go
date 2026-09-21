package main

import "runtime/debug"

// version is set at release build time with -ldflags "-X main.version=...".
var version = "dev"

// buildVersion reports the release version, or the module version when the
// binary was built with `go install module@version`.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
