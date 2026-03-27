package internal

import (
	"testing"
)

func TestGrouping_SameWeekAllPlatforms(t *testing.T) {
	// All three videos are in ISO week 2026-W11 (Mar 9–15)
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "How to Build APIs Fast", Views: 5000, PublishedAt: "2026-03-10T12:00:00Z"},
		{Platform: "tiktok", ID: "tt1", Title: "How to Build APIs Fast #coding", Views: 18000, PublishedAt: "2026-03-11T08:00:00Z"},
		{Platform: "linkedin", ID: "li1", Title: "How to Build APIs Fast", Views: 1500, PublishedAt: "2026-03-12T10:00:00Z"},
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
	if groups[0].ID != "2026-W11" {
		t.Errorf("expected group ID 2026-W11, got %q", groups[0].ID)
	}
	// Canonical title should be YouTube's
	if groups[0].CanonicalTitle != "How to Build APIs Fast" {
		t.Errorf("unexpected canonical title %q", groups[0].CanonicalTitle)
	}
}

func TestGrouping_DifferentWeeksSeparateGroups(t *testing.T) {
	// Week 10 (Mar 2–8) and week 11 (Mar 9–15)
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "Video A", Views: 1000, PublishedAt: "2026-03-02T12:00:00Z"},
		{Platform: "tiktok", ID: "tt1", Title: "Video A", Views: 2000, PublishedAt: "2026-03-03T08:00:00Z"},
		{Platform: "youtube", ID: "yt2", Title: "Video B", Views: 3000, PublishedAt: "2026-03-09T12:00:00Z"},
		{Platform: "tiktok", ID: "tt2", Title: "Video B", Views: 4000, PublishedAt: "2026-03-10T08:00:00Z"},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(unmatched) != 0 {
		t.Fatalf("expected 0 unmatched, got %d", len(unmatched))
	}

	ids := map[string]bool{}
	for _, g := range groups {
		ids[g.ID] = true
	}
	if !ids["2026-W10"] || !ids["2026-W11"] {
		t.Errorf("expected groups 2026-W10 and 2026-W11, got %v", ids)
	}
}

func TestGrouping_NoParsableDateGoesUnmatched(t *testing.T) {
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "My Video", Views: 1000, PublishedAt: "not-a-date"},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}
	if len(unmatched) != 1 {
		t.Errorf("expected 1 unmatched, got %d", len(unmatched))
	}
}

func TestGrouping_MissingDateGoesUnmatched(t *testing.T) {
	videos := []Video{
		{Platform: "linkedin", ID: "li1", Title: "Some Post", Views: 500, PublishedAt: ""},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}
	if len(unmatched) != 1 {
		t.Errorf("expected 1 unmatched, got %d", len(unmatched))
	}
}

func TestGrouping_MultipleVideosFromSamePlatformSameWeek(t *testing.T) {
	// Two YouTube videos in week 11 — should produce 2 sub-groups
	videos := []Video{
		{Platform: "youtube", ID: "yt1", Title: "Video A", Views: 1000, PublishedAt: "2026-03-10T10:00:00Z"},
		{Platform: "youtube", ID: "yt2", Title: "Video B", Views: 2000, PublishedAt: "2026-03-11T10:00:00Z"},
		{Platform: "tiktok", ID: "tt1", Title: "Video A", Views: 3000, PublishedAt: "2026-03-10T12:00:00Z"},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 2 {
		t.Fatalf("expected 2 sub-groups for the week, got %d", len(groups))
	}
	if len(unmatched) != 0 {
		t.Fatalf("expected 0 unmatched, got %d", len(unmatched))
	}

	ids := map[string]bool{}
	for _, g := range groups {
		ids[g.ID] = true
	}
	if !ids["2026-W11"] || !ids["2026-W11-2"] {
		t.Errorf("expected 2026-W11 and 2026-W11-2, got %v", ids)
	}
}

func TestGrouping_SinglePlatformWeekStillAGroup(t *testing.T) {
	// LinkedIn-only week should still produce a group (not unmatched)
	videos := []Video{
		{Platform: "linkedin", ID: "li1", Title: "Solo Post", Views: 400, PublishedAt: "2026-03-10T10:00:00Z"},
	}

	groups, unmatched := Group(videos)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(unmatched) != 0 {
		t.Fatalf("expected 0 unmatched, got %d", len(unmatched))
	}
}

func TestGrouping_CanonicalTitlePrefersYouTube(t *testing.T) {
	videos := []Video{
		{Platform: "tiktok", ID: "tt1", Title: "TikTok Title #shorts", Views: 9000, PublishedAt: "2026-03-10T08:00:00Z"},
		{Platform: "youtube", ID: "yt1", Title: "YouTube Title", Views: 1000, PublishedAt: "2026-03-10T12:00:00Z"},
	}

	groups, _ := Group(videos)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].CanonicalTitle != "YouTube Title" {
		t.Errorf("expected YouTube title as canonical, got %q", groups[0].CanonicalTitle)
	}
}
