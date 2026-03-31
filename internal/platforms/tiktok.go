package platforms

import (
	"fmt"
	"os"

	"github.com/devrel-dashboard/internal"
)

// TikTokFetch fetches all public videos for the configured TikTok username
// using yt-dlp. No API key or login required — yt-dlp must be installed
// (brew install yt-dlp). Pass withComments=true to attempt comment text
// fetching (TikTok support is unreliable; counts will still be populated).
func TikTokFetch(withComments bool) ([]internal.Video, error) {
	username := os.Getenv("TIKTOK_USERNAME")
	if username == "" {
		return nil, fmt.Errorf("tiktok: TIKTOK_USERNAME is not set")
	}

	profileURL := fmt.Sprintf("https://www.tiktok.com/@%s", username)
	return ytdlpFetch("tiktok", profileURL, withComments)
}
