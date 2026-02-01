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
	DryRun  bool `json:"dry_run"`
	Targets struct {
		NarInfos int `json:"nar_infos"`
	} `json:"targets"`

	Removed struct {
		Nars     int `json:"nars"`
		NarInfos int `json:"nar_infos"`

		// We do not record `NoSuchKey` errors as they could be the result of a previous deletion run or multiple
		// narinfos referencing the same nar.
		Errors int `json:"errors"`
	} `json:"removed"`

	MissingInS3 struct {
		Nars     int `json:"nars"`
		NarInfos int `json:"nar_infos"`
	} `json:"missing_in_s3"`
}

func (s *Stats) TotalMissingTargets() int {
	return s.MissingInS3.Nars + s.MissingInS3.NarInfos
}

func (s *Stats) Merge(other *Stats) {
	s.Targets.NarInfos += other.Targets.NarInfos
	s.Removed.Nars += other.Removed.Nars
	s.Removed.NarInfos += other.Removed.NarInfos
	s.Removed.Errors += other.Removed.Errors
	s.MissingInS3.Nars += other.MissingInS3.Nars
	s.MissingInS3.NarInfos += other.MissingInS3.NarInfos
}

func (s *Stats) String() string {
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}

	return string(buf)
}
