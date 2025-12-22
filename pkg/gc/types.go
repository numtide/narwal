package gc

import (
	"encoding/json"
)

type RemovalRecord struct {
	// Key is the full S3 key
	Key string `parquet:"key"`
	// StorePath is the nix store path
	StorePath string `parquet:"store_path,dict"`
	// NotFound indicates whether the object was not found in S3
	NotFound bool `parquet:"not_found"`
	// Error is the error message if the removal failed
	Error string `parquet:"error"`
}

type Stats struct {
	StorePaths struct {
		Targets     int `json:"targets"`
		MissingInS3 int `json:"missing_in_s3"`
		MissingInDB int `json:"missing_in_db"`
	} `json:"store_paths"`

	Removals struct {
		Nars     int `json:"nars"`
		NarInfos int `json:"nar_infos"`
		Errors   int `json:"errors"`
	} `json:"removals"`
}

func (s *Stats) TotalMissingStorePaths() int {
	return s.StorePaths.MissingInS3 + s.StorePaths.MissingInDB
}

func (s *Stats) Merge(other *Stats) {
	s.StorePaths.Targets += other.StorePaths.Targets
	s.StorePaths.MissingInS3 += other.StorePaths.MissingInS3
	s.StorePaths.MissingInDB += other.StorePaths.MissingInDB
	s.Removals.Nars += other.Removals.Nars
	s.Removals.NarInfos += other.Removals.NarInfos
	s.Removals.Errors += other.Removals.Errors
}

func (s *Stats) String() string {
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}

	return string(buf)
}
