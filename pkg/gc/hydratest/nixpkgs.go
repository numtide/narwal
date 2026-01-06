package hydratest

// PackageNames contains real nixpkgs package names for realistic test data.
//
//nolint:gochecknoglobals
var PackageNames = []string{
	"firefox", "chromium", "thunderbird", "libreoffice", "gimp",
	"inkscape", "blender", "vlc", "mpv", "ffmpeg",
	"git", "vim", "neovim", "emacs", "vscode",
	"tmux", "zsh", "fish", "bash", "coreutils",
	"postgresql", "mysql", "sqlite", "redis", "mongodb",
	"nginx", "apache", "caddy", "traefik", "haproxy",
	"docker", "podman", "kubernetes", "terraform", "ansible",
	"python3", "nodejs", "go", "rustc", "gcc",
	"clang", "llvm", "cmake", "ninja", "meson",
	"openssl", "curl", "wget", "jq", "ripgrep",
	"fd", "bat", "exa", "htop", "btop",
	"nix", "nixos-rebuild", "home-manager", "cachix", "nix-prefetch-git",
	"linux", "systemd", "dbus", "udev", "glibc",
	"mesa", "vulkan-loader", "wayland", "xorg-server", "gtk3",
	"qt5", "electron", "steam", "wine", "lutris",
	"aws-cli", "azure-cli", "google-cloud-sdk", "kubectl", "helm",
	"prometheus", "grafana", "loki", "alertmanager", "node-exporter",
	"openssh", "gnupg", "pass", "age", "sops",
	"fontconfig", "freetype", "harfbuzz", "pango", "cairo",
	"zlib", "xz", "zstd", "lz4", "bzip2",
}

// PackageVersions contains realistic version strings.
//
//nolint:gochecknoglobals
var PackageVersions = []string{
	"1.0.0", "1.0.1", "1.1.0", "1.2.0", "1.2.3",
	"2.0.0", "2.1.0", "2.2.0", "2.3.1", "2.4.0",
	"3.0.0", "3.1.0", "3.2.0", "3.3.0", "3.4.0",
	"23.05", "23.11", "24.05", "24.11", "unstable",
	"120.0", "121.0", "122.0", "123.0", "124.0",
}

// Systems contains the supported Nix systems.
//
//nolint:gochecknoglobals
var Systems = []string{
	"x86_64-linux",
	"aarch64-linux",
	"x86_64-darwin",
	"aarch64-darwin",
}

// JobsetNames contains realistic jobset names.
//
//nolint:gochecknoglobals
var JobsetNames = []string{
	"trunk",
	"main",
	"master",
	"develop",
	"staging",
	"release-23.05",
	"release-23.11",
	"release-24.05",
	"release-24.11",
	"nixos-unstable",
	"nixpkgs-unstable",
	"ci",
	"nightly",
	"stable",
	"beta",
}

// OutputNames contains the standard Nix output names.
//
//nolint:gochecknoglobals
var OutputNames = []string{
	"out",
	"lib",
	"dev",
	"doc",
	"man",
	"info",
	"bin",
}

// UserNames contains the test user names.
//
//nolint:gochecknoglobals
var UserNames = []string{
	"alice",
	"bob",
	"charlie",
	"diana",
	"eve",
}

// MachineNames contains realistic builder machine names.
//
//nolint:gochecknoglobals
var MachineNames = []string{
	"builder-01.example.com",
	"builder-02.example.com",
	"builder-03.example.com",
	"aarch64-builder.example.com",
	"darwin-builder.example.com",
	"localhost",
}
