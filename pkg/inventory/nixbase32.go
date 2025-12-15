package inventory

import "errors"

// Nix uses a custom base32 alphabet with characters: 0123456789abcdfghijklmnpqrsvwxyz
// Note: excludes 'e', 'o', 't', 'u' to avoid confusion.
const nixbase32Alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

// nixbase32Lookup returns a 256-byte lookup table for O(1) character decoding.
// Invalid characters map to 0xFF.
func nixbase32Lookup() [256]byte {
	var table [256]byte

	for i := range table {
		table[i] = 0xFF // invalid
	}

	for i, c := range nixbase32Alphabet {
		table[c] = byte(i)
	}

	return table
}

var errInvalidNixbase32 = errors.New("invalid nixbase32 character")

// decodeNixbase32 decodes a 32-character nixbase32 string into a 20-byte hash.
// This is optimized for the fixed sizes used in Nix store paths.
// Uses a lookup table for O(1) character decoding instead of linear search.
func decodeNixbase32Into(dst *[20]byte, src []byte) error {
	if len(src) != 32 {
		return errors.New("nixbase32: input must be 32 bytes")
	}

	// Clear destination
	*dst = [20]byte{}

	lookup := nixbase32Lookup()

	// Decode in reverse order (nixbase32 is little-endian)
	for n := range 32 {
		c := src[31-n]

		digit := lookup[c] //nolint:gosec // c is always valid byte index
		if digit == 0xFF {
			return errInvalidNixbase32
		}

		// Calculate bit position
		b := n * 5
		i := b / 8
		j := b % 8

		// OR the main pattern
		dst[i] |= digit << j

		// Handle carry to next byte
		if j > 3 && i+1 < 20 {
			dst[i+1] |= digit >> (8 - j)
		}
	}

	return nil
}

// decodeNixbase32SHA256Into decodes a 52-character nixbase32 string into a 32-byte SHA256 hash.
// Uses a lookup table for O(1) character decoding.
func decodeNixbase32SHA256Into(dst *[32]byte, src []byte) error {
	if len(src) != 52 {
		return errors.New("nixbase32: SHA256 input must be 52 bytes")
	}

	// Clear destination
	*dst = [32]byte{}

	lookup := nixbase32Lookup()

	// Decode in reverse order (nixbase32 is little-endian)
	for n := range 52 {
		c := src[51-n]

		digit := lookup[c] //nolint:gosec // c is always valid byte index
		if digit == 0xFF {
			return errInvalidNixbase32
		}

		// Calculate bit position
		b := n * 5
		i := b / 8
		j := b % 8

		// OR the main pattern
		dst[i] |= digit << j

		// Handle carry to next byte
		if j > 3 && i+1 < 32 {
			dst[i+1] |= digit >> (8 - j)
		}
	}

	return nil
}

// decodeNixbase32 decodes nixbase32-encoded data into a byte slice.
// Returns the decoded bytes. The output size is calculated as (len(src) * 5 + 7) / 8.
// Uses a lookup table for O(1) character decoding.
func decodeNixbase32(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}

	// Calculate output size: each input char represents 5 bits
	outLen := (len(src) * 5) / 8
	dst := make([]byte, outLen)

	lookup := nixbase32Lookup()

	// Decode in reverse order (nixbase32 is little-endian)
	for n := range src {
		c := src[len(src)-1-n]

		digit := lookup[c]
		if digit == 0xFF {
			return nil, errInvalidNixbase32
		}

		// Calculate bit position
		b := n * 5
		i := b / 8
		j := b % 8

		// OR the main pattern
		if i < outLen {
			dst[i] |= digit << j
		}

		// Handle carry to next byte
		if j > 3 && i+1 < outLen {
			dst[i+1] |= digit >> (8 - j)
		}
	}

	return dst, nil
}
