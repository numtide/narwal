package inventory

type Object struct {
	Bucket           string `parquet:"bucket"`
	Key              string `parquet:"key"`
	Size             int64  `parquet:"size"`
	LastModifiedDate int64  `parquet:"last_modified_date"`
	ETag             string `parquet:"e_tag"`
	StorageClass     string `parquet:"storage_class"`
}

type ObjectEssential struct {
	Bucket string `parquet:"bucket"`
	Key    string `parquet:"key"`
}
