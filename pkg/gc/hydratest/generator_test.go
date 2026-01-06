package hydratest_test

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/numtide/narwal/pkg/gc/hydratest"
)

func TestGenerateNixHash(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(12345)) //nolint:gosec

	hash := hydratest.GenerateNixHash(rng)

	// Check length
	if len(hash) != hydratest.NixHashLength {
		t.Errorf("expected hash length %d, got %d", hydratest.NixHashLength, len(hash))
	}

	// Check all characters are in the alphabet
	for i, c := range hash {
		if !strings.ContainsRune(hydratest.NixBase32Alphabet, c) {
			t.Errorf("invalid character %c at position %d", c, i)
		}
	}
}

func TestGenerateNixHashDeterministic(t *testing.T) {
	t.Parallel()

	rng1 := rand.New(rand.NewSource(12345)) //nolint:gosec
	rng2 := rand.New(rand.NewSource(12345)) //nolint:gosec

	hash1 := hydratest.GenerateNixHash(rng1)
	hash2 := hydratest.GenerateNixHash(rng2)

	if hash1 != hash2 {
		t.Errorf("expected deterministic hash generation, got %s and %s", hash1, hash2)
	}
}

func TestGenerateStorePath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(12345)) //nolint:gosec

	path := hydratest.GenerateStorePath(rng, "firefox", "120.0")

	if !strings.HasPrefix(path, "/nix/store/") {
		t.Errorf("expected path to start with /nix/store/, got %s", path)
	}

	if !strings.Contains(path, "firefox") {
		t.Errorf("expected path to contain firefox, got %s", path)
	}

	if !strings.Contains(path, "120.0") {
		t.Errorf("expected path to contain 120.0, got %s", path)
	}
}

func TestGenerateDrvPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(12345)) //nolint:gosec

	path := hydratest.GenerateDrvPath(rng, "firefox", "120.0")

	if !strings.HasPrefix(path, "/nix/store/") {
		t.Errorf("expected path to start with /nix/store/, got %s", path)
	}

	if !strings.HasSuffix(path, ".drv") {
		t.Errorf("expected path to end with .drv, got %s", path)
	}
}

func TestNixBase32AlphabetIsValid(t *testing.T) {
	t.Parallel()

	// The Nix base32 alphabet should not contain e, o, t, u
	forbidden := "eotu"
	for _, c := range forbidden {
		if strings.ContainsRune(hydratest.NixBase32Alphabet, c) {
			t.Errorf("alphabet should not contain %c", c)
		}
	}

	// Should have exactly 32 characters
	if len(hydratest.NixBase32Alphabet) != 32 {
		t.Errorf("expected alphabet length 32, got %d", len(hydratest.NixBase32Alphabet))
	}
}
