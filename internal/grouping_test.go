package internal

import (
	"testing"
)

func TestGrouping_MatchesSameVideoAcrossPlatforms(t *testing.T) {
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "How to Build APIs Fast", DurationSeconds: 58, Views: 5000},
		{Platform: "tiktok", ID: "tt1", Title: "How to Build APIs Fast #coding #shorts", DurationSeconds: 57, Views: 18000},
		{Platform: "linkedin", ID: "li1", Title: "How to Build APIs Fast", DurationSeconds: 58, Views: 1500},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(unmatched) != 0 {
		t.Fatalf("expected 0 unmatched, got %d", len(unmatched))
	}
	if groups[0].TotalViews != 24500 {
		t.Errorf("expected total views 24500, got %d", groups[0].TotalViews)
	}
	if len(groups[0].Platforms) != 3 {
		t.Errorf("expected 3 platforms, got %d", len(groups[0].Platforms))
	}
}

func TestGrouping_DurationMismatchNotGrouped(t *testing.T) {
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "My Tutorial", DurationSeconds: 30, Views: 1000},
		{Platform: "tiktok", ID: "tt1", Title: "My Tutorial", DurationSeconds: 90, Views: 2000},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 0 {
		t.Errorf("expected 0 groups (duration too different), got %d", len(groups))
	}
	if len(unmatched) != 2 {
		t.Errorf("expected 2 unmatched, got %d", len(unmatched))
	}
}

func TestGrouping_DifferentVideosSamePlatform(t *testing.T) {
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "Video Alpha", DurationSeconds: 45, Views: 100},
		{Platform: "youtube", ID: "yt2", Title: "Video Beta", DurationSeconds: 50, Views: 200},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 0 {
		t.Errorf("expected 0 groups (same platform), got %d", len(groups))
	}
	if len(unmatched) != 2 {
		t.Errorf("expected 2 unmatched, got %d", len(unmatched))
	}
}

func TestGrouping_PartialMatch(t *testing.T) {
	// Video exists on YouTube and TikTok but not LinkedIn
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "Quick Go Tutorial", DurationSeconds: 60, Views: 3000},
		{Platform: "tiktok", ID: "tt1", Title: "Quick Go Tutorial #golang", DurationSeconds: 59, Views: 7000},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if _, ok := groups[0].Platforms["youtube"]; !ok {
		t.Error("expected youtube in group platforms")
	}
	if _, ok := groups[0].Platforms["tiktok"]; !ok {
		t.Error("expected tiktok in group platforms")
	}
	if len(unmatched) != 0 {
		t.Errorf("expected 0 unmatched, got %d", len(unmatched))
	}
}

func TestJaroWinkler(t *testing.T) {
	tests := []struct {
		a, b     string
		wantHigh bool
	}{
		{"hello world", "hello world", true},
		{"hello world", "hello wrold", true},
		{"completely different", "nothing alike xyz", false},
		{"go tutorial shorts", "go tutorial #shorts #coding", true},
	}

	for _, tt := range tests {
		sim := jaroWinkler(normalizeTitle(tt.a), normalizeTitle(tt.b))
		if tt.wantHigh && sim < 0.70 {
			t.Errorf("jaroWinkler(%q, %q) = %.2f, want >= 0.70", tt.a, tt.b, sim)
		}
		if !tt.wantHigh && sim >= 0.70 {
			t.Errorf("jaroWinkler(%q, %q) = %.2f, want < 0.70", tt.a, tt.b, sim)
		}
	}
}
