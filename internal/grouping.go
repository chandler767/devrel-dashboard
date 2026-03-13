package internal

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"unicode"
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

// UnmatchedVideo is a video that only exists on one platform.
type UnmatchedVideo struct {
	Platform        string `json:"platform"`
	VideoID         string `json:"video_id"`
	Title           string `json:"title"`
	Views           int64  `json:"views"`
	DurationSeconds int    `json:"duration_seconds"`
	URL             string `json:"url"`
	PublishedAt     string `json:"published_at"`
}

var (
	hashtagRe    = regexp.MustCompile(`#\S+`)
	nonAlphaRe   = regexp.MustCompile(`[^a-z0-9\s]`)
	whitespaceRe = regexp.MustCompile(`\s+`)
)

// normalizeTitle lowercases, strips hashtags and punctuation, collapses whitespace.
func normalizeTitle(title string) string {
	s := strings.ToLower(title)
	s = hashtagRe.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	s = nonAlphaRe.ReplaceAllString(b.String(), " ")
	s = whitespaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	return s
}

// jaroWinkler computes the Jaro-Winkler similarity between two strings.
// Returns a value between 0.0 (no match) and 1.0 (exact match).
func jaroWinkler(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	if len(s1) == 0 || len(s2) == 0 {
		return 0.0
	}

	// Jaro similarity
	matchDist := int(math.Max(float64(len(s1)), float64(len(s2)))/2) - 1
	if matchDist < 0 {
		matchDist = 0
	}

	s1Matches := make([]bool, len(s1))
	s2Matches := make([]bool, len(s2))

	matches := 0
	transpositions := 0

	for i, c1 := range s1 {
		start := int(math.Max(0, float64(i-matchDist)))
		end := int(math.Min(float64(len(s2)-1), float64(i+matchDist)))
		for j := start; j <= end; j++ {
			if s2Matches[j] || rune(s2[j]) != c1 {
				continue
			}
			s1Matches[i] = true
			s2Matches[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0.0
	}

	k := 0
	for i := range s1 {
		if !s1Matches[i] {
			continue
		}
		for k < len(s2) && !s2Matches[k] {
			k++
		}
		if k < len(s2) && rune(s1[i]) != rune(s2[k]) {
			transpositions++
		}
		k++
	}

	jaro := (float64(matches)/float64(len(s1)) +
		float64(matches)/float64(len(s2)) +
		float64(matches-transpositions/2)/float64(matches)) / 3.0

	// Winkler prefix boost (up to 4 chars)
	prefix := 0
	for i := 0; i < int(math.Min(4, math.Min(float64(len(s1)), float64(len(s2))))); i++ {
		if s1[i] == s2[i] {
			prefix++
		} else {
			break
		}
	}

	return jaro + float64(prefix)*0.1*(1-jaro)
}

// union-find for clustering
type unionFind struct {
	parent []int
}

func newUnionFind(n int) *unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &unionFind{parent: p}
}

func (uf *unionFind) find(x int) int {
	if uf.parent[x] != x {
		uf.parent[x] = uf.find(uf.parent[x])
	}
	return uf.parent[x]
}

func (uf *unionFind) union(x, y int) {
	px, py := uf.find(x), uf.find(y)
	if px != py {
		uf.parent[px] = py
	}
}

const (
	titleSimilarityThreshold = 0.70
	durationDeltaSeconds     = 5
)

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

// Group clusters videos across platforms by title similarity and duration proximity.
// Manual merges from manual_groups.json are applied first, then auto-grouping runs.
// Videos that match go into VideoGroups; those that don't are returned as UnmatchedVideo.
func Group(videos []Video) ([]VideoGroup, []UnmatchedVideo) {
	n := len(videos)
	if n == 0 {
		return nil, nil
	}

	uf := newUnionFind(n)

	// Build a lookup: (platform, id) → index in videos slice
	videoIndex := make(map[string]int, n)
	for i, v := range videos {
		videoIndex[v.Platform+":"+v.ID] = i
	}

	// Apply manual merges first — pre-union any specified pairs
	for _, mg := range loadManualGroups() {
		if len(mg.VideoIDs) < 2 {
			continue
		}
		first := -1
		for _, vid := range mg.VideoIDs {
			idx, ok := videoIndex[vid.Platform+":"+vid.ID]
			if !ok {
				continue
			}
			if first == -1 {
				first = idx
			} else {
				uf.union(first, idx)
			}
		}
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			// Only group videos from different platforms
			if videos[i].Platform == videos[j].Platform {
				continue
			}
			normI := normalizeTitle(videos[i].Title)
			normJ := normalizeTitle(videos[j].Title)
			sim := jaroWinkler(normI, normJ)
			durationDiff := videos[i].DurationSeconds - videos[j].DurationSeconds
			if durationDiff < 0 {
				durationDiff = -durationDiff
			}
			if sim >= titleSimilarityThreshold && durationDiff <= durationDeltaSeconds {
				uf.union(i, j)
			}
		}
	}

	// Build clusters: root → []index
	clusters := map[int][]int{}
	for i := 0; i < n; i++ {
		root := uf.find(i)
		clusters[root] = append(clusters[root], i)
	}

	var groups []VideoGroup
	var unmatched []UnmatchedVideo

	for _, indices := range clusters {
		if len(indices) == 1 {
			v := videos[indices[0]]
			unmatched = append(unmatched, UnmatchedVideo{
				Platform:        v.Platform,
				VideoID:         v.ID,
				Title:           v.Title,
				Views:           v.Views,
				DurationSeconds: v.DurationSeconds,
				URL:             v.URL,
				PublishedAt:     v.PublishedAt,
			})
			continue
		}

		// Check if this cluster actually has multiple platforms
		platformsSeen := map[string]bool{}
		for _, idx := range indices {
			platformsSeen[videos[idx].Platform] = true
		}
		if len(platformsSeen) == 1 {
			// All from same platform - treat as unmatched
			for _, idx := range indices {
				v := videos[idx]
				unmatched = append(unmatched, UnmatchedVideo{
					Platform:        v.Platform,
					VideoID:         v.ID,
					Title:           v.Title,
					Views:           v.Views,
					DurationSeconds: v.DurationSeconds,
					URL:             v.URL,
					PublishedAt:     v.PublishedAt,
				})
			}
			continue
		}

		group := VideoGroup{
			Platforms: map[string]PlatformData{},
		}

		var totalViews int64
		var totalDuration int
		var canonicalTitle string
		var canonicalPriority int // 1=youtube, 2=other

		for _, idx := range indices {
			v := videos[idx]
			totalViews += v.Views
			totalDuration += v.DurationSeconds

			// Pick canonical title: prefer YouTube, then longest
			if v.Platform == "youtube" && canonicalPriority < 1 {
				canonicalTitle = v.Title
				canonicalPriority = 1
			} else if canonicalPriority == 0 && len(v.Title) > len(canonicalTitle) {
				canonicalTitle = v.Title
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
		group.DurationSeconds = totalDuration / len(indices)
		group.TotalViews = totalViews
		group.ID = videoGroupID(canonicalTitle)
		groups = append(groups, group)
	}

	return groups, unmatched
}

// videoGroupID generates a stable short ID from the canonical title.
func videoGroupID(title string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(title))))
	return fmt.Sprintf("%x", h[:4])
}
