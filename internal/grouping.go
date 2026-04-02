package internal

import (
	"time"
)

// Video represents a video fetched from a single platform.
type Video struct {
	Platform        string
	ID              string
	Title           string
	Author          string
	Views           int64
	Likes           int64
	Comments        int64
	Shares          int64
	Clicks          int64
	CommentTexts    []string
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

// VideoGroup holds a single video's data, keyed by platform.
// The platforms map always has exactly one entry (or more for manually-linked
// cross-platform videos defined in manual_groups.json — kept for backwards
// compatibility with existing reports).
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

// UnmatchedVideo holds a video whose published_at could not be parsed.
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

// Group converts each video into its own VideoGroup.
// Videos with missing or unparseable publish dates go to unmatched.
func Group(videos []Video) ([]VideoGroup, []UnmatchedVideo) {
	var groups   []VideoGroup
	var unmatched []UnmatchedVideo

	for _, v := range videos {
		if v.PublishedAt == "" {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}
		if _, err := time.Parse(time.RFC3339, v.PublishedAt); err != nil {
			unmatched = append(unmatched, videoToUnmatched(v))
			continue
		}

		groups = append(groups, VideoGroup{
			ID:              v.Platform + ":" + v.ID,
			CanonicalTitle:  v.Title,
			DurationSeconds: v.DurationSeconds,
			TotalViews:      v.Views,
			TotalLikes:      v.Likes,
			TotalComments:   v.Comments,
			TotalShares:     v.Shares,
			Thumbnail:       v.Thumbnail,
			Description:     v.Description,
			Tags:            v.Tags,
			Platforms: map[string]PlatformData{
				v.Platform: {
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
				},
			},
		})
	}

	return groups, unmatched
}

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
