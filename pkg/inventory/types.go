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
	// Idx is the sequential index from BadgerDB iteration, used for ordering records.
	// Not exported to parquet.
	Idx int64 `parquet:"-"`

	StorePath   string   `parquet:"store_path"`
	URL         string   `parquet:"url"`
	Compression string   `parquet:"compression,dict"`
	FileHash    string   `parquet:"file_hash"`
	FileSize    uint64   `parquet:"file_size"`
	NarHash     string   `parquet:"nar_hash"`
	NarSize     uint64   `parquet:"nar_size"`
	References  []string `parquet:"references,list,dict"`
	Deriver     string   `parquet:"deriver"`
	System      string   `parquet:"system,optional,dict"`
	CA          string   `parquet:"ca,optional,dict"`
	Signatures  []string `parquet:"signatures,list"`

	// last_modified_at is a unix timestamp in millis which has been truncated to the beginning of the week in which
	// the object was last modified.
	LastModifiedAt int64 `parquet:"last_modified_at"`
}
