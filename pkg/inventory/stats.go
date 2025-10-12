package inventory

import (
	"encoding/json"
	"fmt"

	"github.com/dustin/go-humanize"
)

type SizeBytes uint64

func (s SizeBytes) MarshalJSON() ([]byte, error) {
	bytes, err := json.Marshal(humanize.Bytes(uint64(s)))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SizeBytes: %w", err)
	}

	return bytes, nil
}

type Count uint64

func (s Count) MarshalJSON() ([]byte, error) {
	bytes, err := json.Marshal(humanize.Comma(int64(s))) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Count: %w", err)
	}

	return bytes, nil
}

type ObjectStats struct {
	Size  SizeBytes `json:"size,omitempty"`
	Count Count     `json:"count,omitempty"`
}

type NarinfoStats struct {
	Size        SizeBytes `json:"size,omitempty"`
	Count       Count     `json:"count,omitempty"`
	Verified    Count     `json:"verified,omitempty"`
	Missing     Count     `json:"missing,omitempty"`
	Invalid     Count     `json:"invalid,omitempty"`
	BadChecksum Count     `json:"bad_checksum,omitempty"`
	Deleted     Count     `json:"deleted,omitempty"`
}

type Stats struct {
	Objects ObjectStats  `json:"objects"`
	Narinfo NarinfoStats `json:"narinfo"`
}

func (s *Stats) Merge(other *Stats) {
	s.Objects.Size += other.Objects.Size
	s.Objects.Count += other.Objects.Count

	s.Narinfo.Size += other.Narinfo.Size
	s.Narinfo.Count += other.Narinfo.Count
	s.Narinfo.Verified += other.Narinfo.Verified
	s.Narinfo.Missing += other.Narinfo.Missing
	s.Narinfo.Invalid += other.Narinfo.Invalid
	s.Narinfo.BadChecksum += other.Narinfo.BadChecksum
	s.Narinfo.Deleted += other.Narinfo.Deleted
}

func (s *Stats) String() string {
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}

	return string(buf)
}
