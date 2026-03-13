package platforms

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/devrel-dashboard/internal"
)

type ytdlpVideo struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	ViewCount  int64   `json:"view_count"`
	Duration   float64 `json:"duration"`
	WebpageURL string  `json:"webpage_url"`
	UploadDate string  `json:"upload_date"` // "YYYYMMDD"
}

// ytdlpFetch runs yt-dlp against the given URL and returns videos tagged
// with the given platform name. Works for any public URL yt-dlp supports.
func ytdlpFetch(platform, url string) ([]internal.Video, error) {
	cmd := exec.Command("yt-dlp", "--dump-json", "--quiet", "--no-warnings", url)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: pipe: %w", platform, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: yt-dlp not found — install with: brew install yt-dlp\n%w", platform, err)
	}

	var videos []internal.Video
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v ytdlpVideo
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			fmt.Fprintf(os.Stderr, "%s: skipping unparseable line: %v\n", platform, err)
			continue
		}

		publishedAt := ""
		if len(v.UploadDate) == 8 {
			publishedAt = fmt.Sprintf("%s-%s-%sT00:00:00Z",
				v.UploadDate[0:4], v.UploadDate[4:6], v.UploadDate[6:8])
		}

		videos = append(videos, internal.Video{
			Platform:        platform,
			ID:              v.ID,
			Title:           v.Title,
			Views:           v.ViewCount,
			DurationSeconds: int(v.Duration),
			URL:             v.WebpageURL,
			PublishedAt:     publishedAt,
		})
	}

	if err := cmd.Wait(); err != nil {
		if len(videos) == 0 {
			return nil, fmt.Errorf("%s: yt-dlp failed: %w", platform, err)
		}
		fmt.Fprintf(os.Stderr, "%s: yt-dlp exited with warning: %v\n", platform, err)
	}

	return videos, nil
}
