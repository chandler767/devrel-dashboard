package platforms

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/devrel-dashboard/internal"
)

const manualVideosFile = "manual_videos.json"

type manualEntry struct {
	URL  string `json:"url"`
	Note string `json:"note,omitempty"`
}

// ManualFetch reads manual_videos.json and uses yt-dlp to fetch current stats
// for each listed URL. The platform is inferred from the URL.
// If the file doesn't exist, returns nil with no error.
func ManualFetch() ([]internal.Video, error) {
	data, err := os.ReadFile(manualVideosFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manual: read %s: %w", manualVideosFile, err)
	}

	var entries []manualEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("manual: parse %s: %w", manualVideosFile, err)
	}

	var all []internal.Video
	for _, entry := range entries {
		platform := inferPlatform(entry.URL)
		fmt.Printf("  Fetching manual video (%s): %s\n", platform, entry.URL)
		videos, err := ytdlpFetch(platform, entry.URL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "manual: warning: could not fetch %s: %v\n", entry.URL, err)
			continue
		}
		all = append(all, videos...)
	}

	return all, nil
}

func inferPlatform(url string) string {
	switch {
	case strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be"):
		return "youtube"
	case strings.Contains(url, "tiktok.com"):
		return "tiktok"
	case strings.Contains(url, "linkedin.com"):
		return "linkedin"
	default:
		return "other"
	}
}
