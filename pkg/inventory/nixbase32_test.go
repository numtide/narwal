//nolint:testpackage // testing internal functions
package inventory

import (
	"testing"

	"github.com/nix-community/go-nix/pkg/nixbase32"
)

func TestDecodeNixbase32Into(t *testing.T) {
	t.Parallel()

	// Test cases from real nix store paths
	testCases := []string{
		"00bgd045z0d4icpbc2yyz4gx48ak44la", // hello
		"zzz0z6xz6fhxiwnhffkvhc4lfyv1ijwf",
		"0000000000000000000000000000000a",
		"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"abcdfghijklmnpqrsvwxyz0123456789",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			// Decode with reference implementation
			expected, err := nixbase32.DecodeString(tc)
			if err != nil {
				t.Fatalf("reference decode failed: %v", err)
			}

			// Decode with our optimized implementation
			var got [20]byte
			if err := decodeNixbase32Into(&got, []byte(tc)); err != nil {
				t.Fatalf("optimized decode failed: %v", err)
			}

			// Compare
			for i := range expected {
				if got[i] != expected[i] {
					t.Errorf("byte %d: got %02x, want %02x", i, got[i], expected[i])
				}
			}
		})
	}
}

func TestDecodeNixbase32SHA256Into(t *testing.T) {
	t.Parallel()

	// 52-character nixbase32 SHA256 hashes
	testCases := []string{
		"0rmalafq2v3k7a83jcmh8hnh5kilbbzqf1rkzsdz0fv1nayscp04", // NarHash example
		"1s5lj43a6dvnc4g31fhcfzrhyk0spz40v7vi1k308ghkd2zxh8fp", // FileHash example
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()

			// Decode with reference implementation
			expected, err := nixbase32.DecodeString(tc)
			if err != nil {
				t.Fatalf("reference decode failed: %v", err)
			}

			if len(expected) != 32 {
				t.Fatalf("expected 32 bytes, got %d", len(expected))
			}

			// Decode with our optimized implementation
			var got [32]byte
			if err := decodeNixbase32SHA256Into(&got, []byte(tc)); err != nil {
				t.Fatalf("optimized decode failed: %v", err)
			}

			// Compare
			for i := range expected {
				if got[i] != expected[i] {
					t.Errorf("byte %d: got %02x, want %02x", i, got[i], expected[i])
				}
			}
		})
	}
}

func BenchmarkNixbase32Reference(b *testing.B) {
	src := "00bgd045z0d4icpbc2yyz4gx48ak44la"

	b.ResetTimer()

	for b.Loop() {
		_, _ = nixbase32.DecodeString(src)
	}
}

func BenchmarkNixbase32Optimized(b *testing.B) {
	src := []byte("00bgd045z0d4icpbc2yyz4gx48ak44la")

	var dst [20]byte

	b.ResetTimer()

	for b.Loop() {
		_ = decodeNixbase32Into(&dst, src)
	}
}

func BenchmarkNixbase32SHA256Reference(b *testing.B) {
	src := "0rmalafq2v3k7a83jcmh8hnh5kilbbzqf1rkzsdz0fv1nayscp04"

	b.ResetTimer()

	for b.Loop() {
		_, _ = nixbase32.DecodeString(src)
	}
}

func BenchmarkNixbase32SHA256Optimized(b *testing.B) {
	src := []byte("0rmalafq2v3k7a83jcmh8hnh5kilbbzqf1rkzsdz0fv1nayscp04")

	var dst [32]byte

	b.ResetTimer()

	for b.Loop() {
		_ = decodeNixbase32SHA256Into(&dst, src)
	}
}

func TestParseCA(t *testing.T) {
	t.Parallel()

	narinfo := `StorePaths: /nix/store/01ys3jg9mfgf8rsv28ssy7iv0020gxv1-witherable-0.5.drv
URL: nar/1cfhl0zvh3bb91n77mhvs377spcw2d2rpl6kczcpz9jkm3kbhama.nar.xz
Compression: xz
FileHash: sha256:1cfhl0zvh3bb91n77mhvs377spcw2d2rpl6kczcpz9jkm3kbhama
FileSize: 3876
NarHash: sha256:1kyyv4p5mpmzgkz8wccwl3q6yc534bc3h992x5vxsj5wlbrhm0jm
NarSize: 10848
CA: text:sha256:18gl8c1mc5qpf8wkkfvz03xv9f4hg8wp2gzh4c4b7q36h70974zj
Sig: cache.nixos.org-1:z2wS320cmebqeiIgaQRWFJEXUv+pkDBXWMlCbCoH0/GeKBg1RNuKtaOpnzzeQCqh5cnO/NTZmuuSlvZHpfjrCA==
`

	record, err := parseNarinfoToRecord([]byte(narinfo))
	if err != nil {
		t.Fatalf("parseNarinfoToRecord failed: %v", err)
	}

	t.Logf("CAAlgo: %q", record.CAAlgo)

	if record.CAAlgo != "text:sha256" {
		t.Errorf("expected CAAlgo='text:sha256', got %q", record.CAAlgo)
	}

	if record.CAHash == nil {
		t.Fatal("expected CAHash to be non-nil")
	}

	t.Logf("CAHash: %x (len=%d)", *record.CAHash, len(*record.CAHash))

	if len(*record.CAHash) != 32 {
		t.Errorf("expected CAHash length 32, got %d", len(*record.CAHash))
	}
}
