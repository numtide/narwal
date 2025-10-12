package inventory_test

import (
	"testing"
	"time"

	"github.com/numtide/narwal/pkg/inventory"
)

func TestTruncateToWeek(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string // RFC3339 format for readability
		expected string // Expected Monday 00:00:00 UTC
	}{
		{
			name:     "Monday morning",
			input:    "2025-10-06T10:30:00Z", // Monday
			expected: "2025-10-06T00:00:00Z",
		},
		{
			name:     "Tuesday afternoon",
			input:    "2025-10-07T15:45:30Z", // Tuesday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Wednesday",
			input:    "2025-10-08T12:00:00Z", // Wednesday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Thursday",
			input:    "2025-10-09T08:20:15Z", // Thursday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Friday evening",
			input:    "2025-10-10T23:59:59Z", // Friday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Saturday",
			input:    "2025-10-11T14:30:00Z", // Saturday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Sunday",
			input:    "2025-10-12T09:15:00Z", // Sunday
			expected: "2025-10-06T00:00:00Z", // Previous Monday
		},
		{
			name:     "Monday at midnight",
			input:    "2025-10-06T00:00:00Z", // Monday midnight
			expected: "2025-10-06T00:00:00Z", // Same time
		},
		{
			name:     "Sunday just before midnight",
			input:    "2025-10-12T23:59:59.999Z", // Sunday end of week
			expected: "2025-10-06T00:00:00Z",     // Previous Monday
		},
		{
			name:     "Next week Monday",
			input:    "2025-10-13T00:00:00Z", // Next Monday
			expected: "2025-10-13T00:00:00Z", // Same time
		},
		{
			name:     "Different timezone (PST evening becomes UTC next day)",
			input:    "2025-10-06T18:00:00-07:00", // Monday 6 PM PST = Tuesday 1 AM UTC
			expected: "2025-10-06T00:00:00Z",      // Monday UTC
		},
		{
			name:     "Year boundary",
			input:    "2025-01-01T12:00:00Z", // Wednesday, Jan 1, 2025
			expected: "2024-12-30T00:00:00Z", // Previous Monday
		},
		{
			name:     "Leap year",
			input:    "2024-02-29T15:30:00Z", // Thursday, Feb 29, 2024 (leap year)
			expected: "2024-02-26T00:00:00Z", // Previous Monday
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Parse input time
			inputTime, err := time.Parse(time.RFC3339, tt.input)
			if err != nil {
				t.Fatalf("Failed to parse input time: %v", err)
			}

			// Parse expected time
			expectedTime, err := time.Parse(time.RFC3339, tt.expected)
			if err != nil {
				t.Fatalf("Failed to parse expected time: %v", err)
			}

			// Convert to milliseconds and call function
			inputMs := inputTime.UnixMilli()
			resultMs := inventory.TruncateToWeek(inputMs)

			// Convert result back to time for comparison
			resultTime := time.UnixMilli(resultMs)

			// Compare
			if !resultTime.Equal(expectedTime) {
				t.Errorf("TruncateToWeek(%v) = %v, want %v",
					inputTime.Format(time.RFC3339),
					resultTime.Format(time.RFC3339),
					expectedTime.Format(time.RFC3339))
			}

			// Verify it's always Monday at midnight UTC
			resultUTC := resultTime.UTC()
			if resultUTC.Weekday() != time.Monday {
				t.Errorf("Result weekday is %v, want Monday", resultUTC.Weekday())
			}

			if resultUTC.Hour() != 0 || resultUTC.Minute() != 0 || resultUTC.Second() != 0 {
				t.Errorf("Result time is not midnight UTC: %v", resultUTC.Format(time.RFC3339))
			}
		})
	}
}

func TestTruncateToWeek_Idempotent(t *testing.T) {
	t.Parallel()

	// Test that truncating twice gives the same result
	now := time.Now().UnixMilli()
	first := inventory.TruncateToWeek(now)
	second := inventory.TruncateToWeek(first)

	if first != second {
		t.Errorf("TruncateToWeek is not idempotent: first=%d, second=%d", first, second)
	}
}

func TestTruncateToWeek_Sequential(t *testing.T) {
	t.Parallel()

	// Test that all days in a week truncate to the same Monday
	monday, _ := time.Parse(time.RFC3339, "2025-10-06T00:00:00Z")
	mondayMs := monday.UnixMilli()
	expectedTruncated := inventory.TruncateToWeek(mondayMs)

	// Check each day of the week
	for i := range 7 {
		dayMs := monday.Add(time.Duration(i) * 24 * time.Hour).UnixMilli()
		truncated := inventory.TruncateToWeek(dayMs)

		if truncated != expectedTruncated {
			dayTime := time.UnixMilli(dayMs)
			t.Errorf("Day %v (%v) truncated to %v, expected %v",
				i, dayTime.Weekday(),
				time.UnixMilli(truncated).Format(time.RFC3339),
				time.UnixMilli(expectedTruncated).Format(time.RFC3339))
		}
	}
}
