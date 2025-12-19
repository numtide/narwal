package hydratest

import (
	"math/rand"
)

// NixBase32Alphabet is the base32 alphabet used by Nix for store paths.
// It excludes e, o, t, u to avoid confusion with similar-looking characters.
const NixBase32Alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

// NixHashLength is the length of a Nix store path hash (32 characters).
const NixHashLength = 32

// GenerateNixHash generates a random but valid-looking Nix base32 hash.
func GenerateNixHash(rng *rand.Rand) string {
	hash := make([]byte, NixHashLength)
	for i := range hash {
		hash[i] = NixBase32Alphabet[rng.Intn(len(NixBase32Alphabet))]
	}

	return string(hash)
}

// GenerateStorePath generates a Nix store path for a package with the given name and version.
func GenerateStorePath(rng *rand.Rand, name, version string) string {
	hash := GenerateNixHash(rng)

	return "/nix/store/" + hash + "-" + name + "-" + version
}

// GenerateDrvPath generates a Nix derivation path for a package.
func GenerateDrvPath(rng *rand.Rand, name, version string) string {
	hash := GenerateNixHash(rng)

	return "/nix/store/" + hash + "-" + name + "-" + version + ".drv"
}
