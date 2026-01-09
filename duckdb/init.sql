-- DuckDB UDFs for Narwal
-- Equivalent to ClickHouse user-defined functions for Nix base32 encoding

-- nixbase32: Encode binary blob to Nix base32 string
--
-- Nix uses a custom base32 alphabet: 0123456789abcdfghijklmnpqrsvwxyz
-- (excludes e, o, t, u to avoid confusion)
--
-- The encoding is little-endian: LSB of input maps to last char of output
-- Output length: ceil(input_bytes * 8 / 5)
--   20 bytes (SHA1)   -> 32 characters
--   32 bytes (SHA256) -> 52 characters
--
-- Usage:
--   SELECT nixbase32(file_hash) FROM narinfos;
--   SELECT nixbase32(unhex('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'));
CREATE OR REPLACE MACRO nixbase32(input) AS (
    SELECT string_agg(
        '0123456789abcdfghijklmnpqrsvwxyz'[
            ((
                (COALESCE(byte_arr[bit_pos // 8 + 1], 0) >> (bit_pos % 8)) |
                (CASE WHEN (bit_pos % 8) > 3
                      THEN COALESCE(byte_arr[bit_pos // 8 + 2], 0) << (8 - (bit_pos % 8))
                      ELSE 0 END)
            ) & 31) + 1
        ],
        '' ORDER BY n
    )
    FROM (
        SELECT
            n,
            (out_len - 1 - n) * 5 as bit_pos,
            byte_arr
        FROM (
            SELECT
                ((octet_length(input)::bigint * 8 + 4) // 5)::bigint as out_len,
                list_transform(
                    generate_series(0::bigint, octet_length(input)::bigint - 1),
                    i -> (instr('0123456789abcdef', lower(hex(input))[i*2+1]) - 1) * 16 +
                         (instr('0123456789abcdef', lower(hex(input))[i*2+2]) - 1)
                ) as byte_arr
        ) params,
        generate_series(0::bigint, out_len - 1) AS t(n)
    )
);

-- nixbase32_hex: Encode hex string to Nix base32 string
-- Convenience wrapper for when input is already a hex string
--
-- Usage:
--   SELECT nixbase32_hex('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855');
CREATE OR REPLACE MACRO nixbase32_hex(hex_input) AS (
    nixbase32(unhex(hex_input))
);

-- narURL: Construct NAR URL path from file hash and compression type
--
-- Reconstructs the URL field from a narinfo, which is derived from:
--   nar/<nixbase32(file_hash)>.nar[.<compression_ext>]
--
-- Compression mapping:
--   '' or 'none' -> .nar
--   'bzip2'      -> .nar.bz2
--   'xz'         -> .nar.xz
--   'zstd'       -> .nar.zstd
--
-- Usage:
--   SELECT narURL(file_hash, compression) FROM narinfos;
CREATE OR REPLACE MACRO narURL(file_hash, compression) AS (
    'nar/' || nixbase32(file_hash) || '.nar' ||
    CASE
        WHEN compression = '' OR compression = 'none' THEN ''
        WHEN compression = 'bzip2' THEN '.bz2'
        ELSE '.' || compression
    END
);
