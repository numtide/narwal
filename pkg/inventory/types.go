package inventory

type Object struct {
	Bucket           string `parquet:"bucket"`
	Key              string `parquet:"key"`
	Size             int64  `parquet:"size"`
	LastModifiedDate int64  `parquet:"last_modified_date"`
	ETag             string `parquet:"e_tag"`
	StorageClass     string `parquet:"storage_class"`
}

// NarInfoRecord represents a parsed .narinfo file for parquet export.
type NarInfoRecord struct {
	// Hash is the decoded binary store path hash (20 bytes from 32-char nixbase32).
	// e.g., decoded from "00bgd045z0d4icpbc2yyz4gx48ak44la" in "/nix/store/00bgd045z0d4icpbc2yyz4gx48ak44la-foo-1.0"
	Hash [20]byte `parquet:"hash"`

	// Pname is the package name portion of the store path.
	// e.g., "foo-1.0" from "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-foo-1.0"
	Pname string `parquet:"pname,dict"`

	Compression string `parquet:"compression,dict"`

	// FileHash is the binary SHA256 hash of the compressed NAR file.
	// The original "sha256:..." prefix is stripped and validated during parsing.
	FileHash [32]byte `parquet:"file_hash"`
	FileSize uint64   `parquet:"file_size"`

	// NarHash is the binary SHA256 hash of the uncompressed NAR.
	// The original "sha256:..." prefix is stripped and validated during parsing.
	NarHash    [32]byte   `parquet:"nar_hash"`
	NarSize    uint64     `parquet:"nar_size"`
	References [][20]byte `parquet:"references,list,dict"`

	// Deriver is the decoded binary hash from the deriver path (20 bytes from 32-char nixbase32).
	// The "-name.drv" suffix is stripped. Zero value if "unknown-deriver".
	Deriver [20]byte `parquet:"deriver"`

	// DeriverPname is the name portion of the deriver path (without .drv suffix).
	// e.g., "foo-1.0" from "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-foo-1.0.drv"
	// Empty string if "unknown-deriver".
	DeriverPname string `parquet:"deriver_pname,optional,dict"`

	System string `parquet:"system,optional,enum"`

	// SignatureDomains contains the signing key names (e.g., "cache.nixos.org-1").
	// Dictionary-encoded for efficient storage since most records share the same signer.
	SignatureDomains []string `parquet:"signature_domains,list,dict"`

	// SignatureValues contains the decoded Ed25519 signature values (64 bytes each).
	// Stored as fixed-size binary for efficient storage.
	SignatureValues [][64]byte `parquet:"signature_values,list"`

	// CAAlgo is the content address algorithm (e.g., "fixed:sha256", "fixed:r:sha256", "text:sha256").
	// Empty string if no CA field. Dictionary-encoded for efficient storage.
	CAAlgo string `parquet:"ca_algo,optional,enum"`

	// CAHash is the decoded binary content address hash.
	// Length varies by algorithm: md5=16, sha1=20, sha256=32, sha512=64 bytes.
	// Nil if no CA field. Must be pointer for parquet-go optional byte arrays.
	CAHash *[]byte `parquet:"ca_hash,optional"`

	// QuirkReferencesOutOfOrder is true if the original narinfo had references
	// not in canonical order (lexicographic by full basename). Nix stores
	// references in a std::set<StorePaths> which maintains sorted order.
	QuirkReferencesOutOfOrder bool `parquet:"quirk_references_out_of_order"`
}
