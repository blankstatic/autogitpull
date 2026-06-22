package web

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
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

func TestActivityStartUsesMonday(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	startFromMonday := activityStart(time.Date(2026, 6, 22, 0, 0, 0, 0, loc))
	startFromSunday := activityStart(time.Date(2026, 6, 28, 0, 0, 0, 0, loc))
	want := time.Date(2025, 6, 23, 0, 0, 0, 0, loc)

	if !startFromMonday.Equal(want) {
		t.Fatalf("expected Monday start %s, got %s", want, startFromMonday)
	}
	if !startFromSunday.Equal(want) {
		t.Fatalf("expected Sunday to use previous Monday start %s, got %s", want, startFromSunday)
	}
	if startFromMonday.Weekday() != time.Monday || startFromSunday.Weekday() != time.Monday {
		t.Fatalf("expected activity starts to be Mondays: %s, %s", startFromMonday.Weekday(), startFromSunday.Weekday())
	}
}

func TestEventFilterDefaultsToChanges(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)

	if got := eventFilterFromRequest(req); got != eventFilterChanges {
		t.Fatalf("expected default filter %q, got %q", eventFilterChanges, got)
	}
}

func TestEventFilterURLs(t *testing.T) {
	filter := newEventFilter("/repo", url.Values{"path": []string{"/repo/a"}}, eventFilterAll)

	options := map[string]eventFilterOption{}
	for _, option := range filter.Options {
		options[option.Label] = option
	}

	if options["Changes"].URL != "/repo?path=%2Frepo%2Fa" {
		t.Fatalf("unexpected changes url: %q", options["Changes"].URL)
	}
	if options["Error"].URL != "/repo?filter=error&path=%2Frepo%2Fa" {
		t.Fatalf("unexpected error url: %q", options["Error"].URL)
	}
	if options["All"].URL != "/repo?filter=all&path=%2Frepo%2Fa" {
		t.Fatalf("unexpected all url: %q", options["All"].URL)
	}
	if options["All"].Class != "filter-link active" || options["Changes"].Class != "filter-link" {
		t.Fatalf("unexpected filter options: %+v", options)
	}
}

func TestHumanizeNumber(t *testing.T) {
	tests := map[int]string{
		999:     "999",
		1000:    "1k",
		1500:    "1.5k",
		100000:  "100k",
		1250000: "1.2m",
		-1200:   "-1.2k",
	}
	for input, want := range tests {
		if got := humanizeNumber(input); got != want {
			t.Fatalf("humanizeNumber(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestRepoURLCanIncludeFilter(t *testing.T) {
	if got := repoURL("/repo/a", eventFilterAll); got != "/repo?filter=all&path=/repo/a" {
		t.Fatalf("unexpected repo url: %q", got)
	}
}

func TestRepoCardsAttachLatestUpdate(t *testing.T) {
	repos := []config.RepoInfo{{Path: "/repo/a", Name: "a"}, {Path: "/repo/b", Name: "b"}}
	latest := map[string]db.Update{"/repo/a": {RepoPath: "/repo/a", Status: "error"}}

	cards := repoCards(repos, latest)
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	if cards[0].LastUpdate == nil || cards[0].LastUpdate.Status != "error" {
		t.Fatalf("expected latest update on first card: %+v", cards[0])
	}
	if cards[1].LastUpdate != nil {
		t.Fatalf("expected no latest update on second card: %+v", cards[1])
	}
}
