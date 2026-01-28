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
	Targets struct {
		NarInfos    int `json:"nar_infos"`
		MissingInS3 struct {
			Nars     int `json:"nars"`
			NarInfos int `json:"nar_infos"`
		} `json:"missing_in_s3"`
	} `json:"targets"`

	Removals struct {
		Nars     int `json:"nars"`
		NarInfos int `json:"nar_infos"`
		Errors   int `json:"errors"`
	} `json:"removals"`
}

func (s *Stats) TotalMissingTargets() int {
	return s.Targets.MissingInS3.Nars + s.Targets.MissingInS3.NarInfos
}

func (s *Stats) Merge(other *Stats) {
	s.Targets.NarInfos += other.Targets.NarInfos
	s.Targets.MissingInS3.Nars += other.Targets.MissingInS3.Nars
	s.Targets.MissingInS3.NarInfos += other.Targets.MissingInS3.NarInfos
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
