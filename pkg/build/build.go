// Package build contains constants and values set at build time via -X flags.
package build

//nolint:gochecknoglobals
var (
	// Name is the program name, typically set via Nix to match the derivation's `pname`.
	Name = "nix-binary-cache"
	// Version is the program version, typically set via Nix to match the derivation's `version`.
	Version = "v0.1.0+dev"
)
