package inventory

// S3InventoryRecord represents a record in the S3 inventory parquet file.
// Based on the schema from the manifest: bucket, key, size, last_modified_date, e_tag, storage_class.
type S3InventoryRecord struct {
	Bucket           string `parquet:"bucket"`
	Key              string `parquet:"key"`
	Size             *int64 `parquet:"size"`               // Optional field
	LastModifiedDate *int64 `parquet:"last_modified_date"` // Optional timestamp in millis
	ETag             string `parquet:"e_tag"`              // Optional field
	StorageClass     string `parquet:"storage_class"`      // Optional field
}
