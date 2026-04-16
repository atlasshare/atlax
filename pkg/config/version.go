package config

// Build metadata injected at link time via `-ldflags -X`. See the
// Makefile for the full go build invocation. Defaults (the "dev"
// strings below) apply to `go build` without ldflags, so tests and
// IDE builds still get sensible values.
//
// These are vars rather than consts because the linker can only set
// values on mutable symbols.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
