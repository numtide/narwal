package inventory

type ObjectEssential struct {
	Bucket string `json:"bucket" parquet:"name=bucket, type=BYTE_ARRAY, convertedtype=UTF8"`
	Key    string `json:"key"    parquet:"name=key, type=BYTE_ARRAY, convertedtype=UTF8"`
}

//nolint:lll
type Object struct {
	Bucket         string `json:"bucket" parquet:"name=bucket, type=BYTE_ARRAY, convertedtype=UTF8"`
	Key            string `json:"key"    parquet:"name=key, type=BYTE_ARRAY, convertedtype=UTF8"`
	Size           int64  `json:"size"               parquet:"name=size, type=INT64, repetitiontype=OPTIONAL"`
	LastModifiedAt int64  `json:"last_modified_date" parquet:"name=last_modified_date, type=INT64, repetitiontype=OPTIONAL"`
	ETag           string `json:"e_tag"              parquet:"name=e_tag, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	StorageClass   string `json:"storage_class"      parquet:"name=storage_class, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
}
