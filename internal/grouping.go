package internal

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// ManualGroup is one entry in manual_groups.json.
type ManualGroup struct {
	Note     string          `json:"note"`
	VideoIDs []ManualVideoID `json:"video_ids"`
}

// ManualVideoID identifies a specific video by platform + ID.
type ManualVideoID struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
}

// Video represents a video fetched from a single platform.
type Video struct {
	Platform        string
	ID              string
	Title           string
	Author          string // optional; populated where available (e.g. LinkedIn)
	Views           int64
	DurationSeconds int
	URL             string
	PublishedAt     string
}

// PlatformData holds per-platform data within a VideoGroup.
type PlatformData struct {
	VideoID         string `json:"video_id"`
	Title           string `json:"title"`
	Views           int64  `json:"views"`
	URL             string `json:"url"`
	PublishedAt     string `json:"published_at"`
	DurationSeconds int    `json:"duration_seconds"`
}

// VideoGroup represents a single video that may exist on multiple platforms.
type VideoGroup struct {
	ID              string                  `json:"id"`
	CanonicalTitle  string                  `json:"canonical_title"`
	DurationSeconds int                     `json:"duration_seconds"`
	TotalViews      int64                   `json:"total_views"`
	Platforms       map[string]PlatformData `json:"platforms"`
}

// UnmatchedVideo is a video that could not be assigned to a week group
// (e.g. missing or unparseable publish date).
type UnmatchedVideo struct {
	Platform        string `json:"platform"`
	VideoID         string `json:"video_id"`
	Title           string `json:"title"`
	Views           int64  `json:"views"`
	DurationSeconds int    `json:"duration_seconds"`
	URL             string `json:"url"`
	PublishedAt     string `json:"published_at"`
}

// loadManualGroups reads manual_groups.json if it exists; returns empty slice otherwise.
func loadManualGroups() []ManualGroup {
	data, err := os.ReadFile("manual_groups.json")
	if err != nil {
		return nil
	}
	var groups []ManualGroup
	_ = json.Unmarshal(data, &groups)
	return groups
}

// Group clusters videos by ISO week across platforms.
// Manual merges from manual_groups.json are applied first and bypass week grouping.
// Videos without a parseable publish date are returned as UnmatchedVideo.
func Group(videos []Video) ([]VideoGroup, []UnmatchedVideo) {
	n := len(videos)
	if n == 0 {
		return nil, nil
	}

	videoIndex := make(map[string]int, n)
	for i, v := range videos {
		videoIndex[v.Platform+":"+v.ID] = i
	}

	manuallyGrouped := make(map[int]bool)
	var groups []VideoGroup

	// Apply manual groups first — they override week grouping
	for _, mg := range loadManualGroups() {
		if len(mg.VideoIDs) < 2 {
			continue
		}
		var indices []int
		for _, vid := range mg.VideoIDs {
			if idx, ok := videoIndex[vid.Platform+":"+vid.ID]; ok {
				indices = append(indices, idx)
				manuallyGrouped[idx] = true
			}
		}
		if len(indices) == 0 {
			continue
		}
		g := buildVideoGroup(videos, indices)
		g.ID = videoGroupID(g.CanonicalTitle)
		groups = append(groups, g)
	}

	// Bucket remaining videos by ISO week (year + week number)
	weekBuckets := map[string][]int{}
	var unmatched []UnmatchedVideo

	for i, v := range videos {
		if manuallyGrouped[i] {
			continue
		}
		if v.PublishedAt == "" {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}
		t, err := time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}
		year, week := t.ISOWeek()
		key := fmt.Sprintf("%d-W%02d", year, week)
		weekBuckets[key] = append(weekBuckets[key], i)
	}

	// Build VideoGroups from week buckets.
	// If multiple videos from the same platform land in the same week,
	// they spill into numbered sub-groups (2026-W11-2, etc.).
	for weekKey, indices := range weekBuckets {
		subGroups := splitIntoPlatformGroups(videos, indices)
		for si, sg := range subGroups {
			g := buildVideoGroup(videos, sg)
			if si == 0 {
				g.ID = weekKey
			} else {
				g.ID = fmt.Sprintf("%s-%d", weekKey, si+1)
			}
			groups = append(groups, g)
		}
	}

	return groups, unmatched
}

// buildVideoGroup assembles a VideoGroup from a slice of video indices.
// Canonical title prefers YouTube, then TikTok, then first available.
// Duration prefers YouTube, then TikTok (LinkedIn reports 0).
func buildVideoGroup(videos []Video, indices []int) VideoGroup {
	group := VideoGroup{Platforms: map[string]PlatformData{}}
	var totalViews int64
	var canonicalTitle string
	var canonicalPriority int // 1=youtube, 2=tiktok, 3=other

	for _, idx := range indices {
		v := videos[idx]
		totalViews += v.Views

		switch {
		case v.Platform == "youtube":
			canonicalTitle = v.Title
			canonicalPriority = 1
		case v.Platform == "tiktok" && canonicalPriority != 1:
			canonicalTitle = v.Title
			canonicalPriority = 2
		case canonicalPriority == 0:
			canonicalTitle = v.Title
			canonicalPriority = 3
		}

		group.Platforms[v.Platform] = PlatformData{
			VideoID:         v.ID,
			Title:           v.Title,
			Views:           v.Views,
			URL:             v.URL,
			PublishedAt:     v.PublishedAt,
			DurationSeconds: v.DurationSeconds,
		}
	}

	group.CanonicalTitle = canonicalTitle
	group.TotalViews = totalViews

	if pd, ok := group.Platforms["youtube"]; ok {
		group.DurationSeconds = pd.DurationSeconds
	} else if pd, ok := group.Platforms["tiktok"]; ok {
		group.DurationSeconds = pd.DurationSeconds
	}

	return group
}

// videoToUnmatched converts a Video to an UnmatchedVideo.
func videoToUnmatched(v Video) UnmatchedVideo {
	return UnmatchedVideo{
		Platform:        v.Platform,
		VideoID:         v.ID,
		Title:           v.Title,
		Views:           v.Views,
		DurationSeconds: v.DurationSeconds,
		URL:             v.URL,
		PublishedAt:     v.PublishedAt,
	}
}

// splitIntoPlatformGroups divides video indices into sub-groups where each
// sub-group has at most one video per platform. Videos are sorted by date first
// so that same-day videos from different platforms are preferentially grouped
// together. This prevents TikTok videos (which may have slightly different UTC
// dates) from drifting into adjacent groups.
func splitIntoPlatformGroups(videos []Video, indices []int) [][]int {
	sorted := make([]int, len(indices))
	copy(sorted, indices)
	sort.Slice(sorted, func(a, b int) bool {
		return videos[sorted[a]].PublishedAt < videos[sorted[b]].PublishedAt
	})

	dayOf := func(idx int) string {
		if len(videos[idx].PublishedAt) >= 10 {
			return videos[idx].PublishedAt[:10]
		}
		return ""
	}

	var subGroups [][]int
	for _, idx := range sorted {
		platform := videos[idx].Platform
		day := dayOf(idx)

		// Prefer a sub-group that already has a video from the same calendar day
		// and doesn't yet have this platform. Fall back to a new sub-group rather
		// than placing into a different-day group, so cross-day drift is avoided.
		bestGroup := -1
		for si, sg := range subGroups {
			hasConflict := false
			hasSameDay := false
			for _, sIdx := range sg {
				if videos[sIdx].Platform == platform {
					hasConflict = true
					break
				}
				if dayOf(sIdx) == day {
					hasSameDay = true
				}
			}
			if hasConflict {
				continue
			}
			if hasSameDay {
				bestGroup = si
				break
			}
		}

		if bestGroup >= 0 {
			subGroups[bestGroup] = append(subGroups[bestGroup], idx)
		} else {
			subGroups = append(subGroups, []int{idx})
		}
	}
	return subGroups
}

// videoGroupID generates a stable short ID from the canonical title.
// Used only for manually-defined groups.
func videoGroupID(title string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(title))))
	return fmt.Sprintf("%x", h[:4])
}
