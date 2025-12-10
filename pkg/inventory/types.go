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
	// Hash is the decoded store path hash (20 bytes).
	// Decoded from the 32-char nixbase32 string in the store path.
	Hash []byte `parquet:"hash"`

	// Pname is the package name portion of the store path.
	// e.g., "foo-1.0" from "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-foo-1.0"
	Pname string `parquet:"pname,dict"`

	URL             string   `parquet:"url"`
	Compression     string   `parquet:"compression,dict"`
	FileHash        string   `parquet:"file_hash"`
	FileSize        uint64   `parquet:"file_size"`
	NarHash         string   `parquet:"nar_hash"`
	NarSize         uint64   `parquet:"nar_size"`
	ReferenceHashes [][]byte `parquet:"reference_hashes,list,dict"`
	ReferencePnames []string `parquet:"reference_pnames,list,dict"`
	Deriver         string   `parquet:"deriver"`
	System          string   `parquet:"system,optional,dict"`
	CA              string   `parquet:"ca,optional,dict"`
	Signatures      []string `parquet:"signatures,list"`
}
