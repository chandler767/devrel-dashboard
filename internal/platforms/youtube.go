package platforms

import (
	"fmt"
	"os"
	"strings"

	"github.com/devrel-dashboard/internal"
)

// YouTubeFetch fetches all YouTube Shorts for the configured channel handle
// using yt-dlp. No API key or login required — yt-dlp must be installed
// (brew install yt-dlp).
func YouTubeFetch() ([]internal.Video, error) {
	handle := os.Getenv("YOUTUBE_HANDLE")
	if handle == "" {
		return nil, fmt.Errorf("youtube: YOUTUBE_HANDLE is not set")
	}

	// The /shorts tab fetches only Shorts, skipping long-form videos.
	handle = strings.TrimPrefix(handle, "@")
	shortsURL := fmt.Sprintf("https://www.youtube.com/@%s/shorts", handle)

	return ytdlpFetch("youtube", shortsURL)
}
