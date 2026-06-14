package web

import (
	"testing"
	"time"
)

func TestNewActivitySummaryCountsOnlyProvidedChangedTimes(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	start := time.Date(2026, 6, 7, 0, 0, 0, 0, loc)
	end := time.Date(2026, 6, 13, 0, 0, 0, 0, loc)

	summary := newActivitySummary([]time.Time{
		time.Date(2026, 6, 8, 10, 0, 0, 0, loc),
		time.Date(2026, 6, 8, 12, 0, 0, 0, loc),
		time.Date(2026, 6, 13, 23, 59, 59, 0, loc),
		time.Date(2026, 6, 14, 0, 0, 0, 0, loc),
	}, start, end, loc)

	if summary.Total != 3 {
		t.Fatalf("expected 3 changed updates, got %d", summary.Total)
	}
	if len(summary.Cells) != 7 {
		t.Fatalf("expected 7 cells, got %d", len(summary.Cells))
	}
	if summary.Cells[1].Count != 2 || summary.Cells[1].Level != 2 {
		t.Fatalf("unexpected June 8 cell: %+v", summary.Cells[1])
	}
	if summary.Cells[6].Count != 1 || summary.Cells[6].Level != 1 {
		t.Fatalf("unexpected June 13 cell: %+v", summary.Cells[6])
	}
}
