package internal

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
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
	Likes           int64
	Comments        int64
	Shares          int64
	Clicks          int64    // LinkedIn only
	CommentTexts    []string // top ~20 comments as "Author: text" strings
	Thumbnail       string
	Description     string
	Tags            []string
	DurationSeconds int
	URL             string
	PublishedAt     string
}

// PlatformData holds per-platform data within a VideoGroup.
type PlatformData struct {
	VideoID         string   `json:"video_id"`
	Title           string   `json:"title"`
	Views           int64    `json:"views"`
	Likes           int64    `json:"likes"`
	Comments        int64    `json:"comments"`
	Shares          int64    `json:"shares"`
	Clicks          int64    `json:"clicks,omitempty"`
	CommentTexts    []string `json:"comment_texts,omitempty"`
	Thumbnail       string   `json:"thumbnail,omitempty"`
	Description     string   `json:"description,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	URL             string   `json:"url"`
	PublishedAt     string   `json:"published_at"`
	DurationSeconds int      `json:"duration_seconds"`
}

// VideoGroup represents a single video that may exist on multiple platforms.
type VideoGroup struct {
	ID              string                  `json:"id"`
	CanonicalTitle  string                  `json:"canonical_title"`
	DurationSeconds int                     `json:"duration_seconds"`
	TotalViews      int64                   `json:"total_views"`
	TotalLikes      int64                   `json:"total_likes"`
	TotalComments   int64                   `json:"total_comments"`
	TotalShares     int64                   `json:"total_shares"`
	Thumbnail       string                  `json:"thumbnail,omitempty"`
	Description     string                  `json:"description,omitempty"`
	Tags            []string                `json:"tags,omitempty"`
	Platforms       map[string]PlatformData `json:"platforms"`
}

// UnmatchedVideo is a video that could not be assigned to a week group
// (e.g. missing or unparseable publish date).
type UnmatchedVideo struct {
	Platform        string   `json:"platform"`
	VideoID         string   `json:"video_id"`
	Title           string   `json:"title"`
	Views           int64    `json:"views"`
	Likes           int64    `json:"likes"`
	Comments        int64    `json:"comments"`
	Shares          int64    `json:"shares"`
	Clicks          int64    `json:"clicks,omitempty"`
	CommentTexts    []string `json:"comment_texts,omitempty"`
	Thumbnail       string   `json:"thumbnail,omitempty"`
	Description     string   `json:"description,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	DurationSeconds int      `json:"duration_seconds"`
	URL             string   `json:"url"`
	PublishedAt     string   `json:"published_at"`
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

// Group builds a VideoGroup per video. Manual groups from manual_groups.json
// are applied first (merging explicitly-linked videos across platforms).
// Every remaining video becomes its own single-platform VideoGroup.
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

	// Apply manual groups first
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

	// Each remaining video becomes its own VideoGroup
	var unmatched []UnmatchedVideo
	for i, v := range videos {
		if manuallyGrouped[i] {
			continue
		}
		if v.PublishedAt == "" {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}
		if _, err := time.Parse(time.RFC3339, v.PublishedAt); err != nil {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}
		g := buildVideoGroup(videos, []int{i})
		g.ID = videoGroupID(v.Platform + ":" + v.ID)
		groups = append(groups, g)
	}

	return groups, unmatched
}

// buildVideoGroup assembles a VideoGroup from a slice of video indices.
// Canonical title/thumbnail/description prefer YouTube, then TikTok, then first available.
// Duration prefers YouTube, then TikTok (LinkedIn reports 0).
// Tags are the deduplicated union across all platforms.
func buildVideoGroup(videos []Video, indices []int) VideoGroup {
	group := VideoGroup{Platforms: map[string]PlatformData{}}
	var totalViews, totalLikes, totalComments, totalShares int64
	var canonicalTitle, canonicalThumbnail, canonicalDescription string
	var canonicalPriority int // 1=youtube, 2=tiktok, 3=other
	tagSet := map[string]bool{}

	for _, idx := range indices {
		v := videos[idx]
		totalViews += v.Views
		totalLikes += v.Likes
		totalComments += v.Comments
		totalShares += v.Shares

		switch {
		case v.Platform == "youtube":
			canonicalTitle = v.Title
			canonicalThumbnail = v.Thumbnail
			canonicalDescription = v.Description
			canonicalPriority = 1
		case v.Platform == "tiktok" && canonicalPriority != 1:
			canonicalTitle = v.Title
			canonicalThumbnail = v.Thumbnail
			canonicalDescription = v.Description
			canonicalPriority = 2
		case canonicalPriority == 0:
			canonicalTitle = v.Title
			canonicalThumbnail = v.Thumbnail
			canonicalDescription = v.Description
			canonicalPriority = 3
		}

		for _, tag := range v.Tags {
			tagSet[tag] = true
		}

		group.Platforms[v.Platform] = PlatformData{
			VideoID:         v.ID,
			Title:           v.Title,
			Views:           v.Views,
			Likes:           v.Likes,
			Comments:        v.Comments,
			Shares:          v.Shares,
			Clicks:          v.Clicks,
			CommentTexts:    v.CommentTexts,
			Thumbnail:       v.Thumbnail,
			Description:     v.Description,
			Tags:            v.Tags,
			URL:             v.URL,
			PublishedAt:     v.PublishedAt,
			DurationSeconds: v.DurationSeconds,
		}
	}

	// Deduplicated union of tags
	var tags []string
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	group.CanonicalTitle = canonicalTitle
	group.TotalViews = totalViews
	group.TotalLikes = totalLikes
	group.TotalComments = totalComments
	group.TotalShares = totalShares
	group.Thumbnail = canonicalThumbnail
	group.Description = canonicalDescription
	group.Tags = tags

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
		Likes:           v.Likes,
		Comments:        v.Comments,
		Shares:          v.Shares,
		Clicks:          v.Clicks,
		CommentTexts:    v.CommentTexts,
		Thumbnail:       v.Thumbnail,
		Description:     v.Description,
		Tags:            v.Tags,
		DurationSeconds: v.DurationSeconds,
		URL:             v.URL,
		PublishedAt:     v.PublishedAt,
	}
}


// videoGroupID generates a stable short ID from the canonical title.
// Used only for manually-defined groups.
func videoGroupID(title string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(title))))
	return fmt.Sprintf("%x", h[:4])
}
