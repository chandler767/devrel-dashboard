package platforms

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devrel-dashboard/internal"
)

type ytdlpVideo struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	ViewCount    int64          `json:"view_count"`
	LikeCount    int64          `json:"like_count"`
	CommentCount int64          `json:"comment_count"`
	RepostCount  int64          `json:"repost_count"` // TikTok shares; not public on YouTube
	Duration     float64        `json:"duration"`
	WebpageURL   string         `json:"webpage_url"`
	Thumbnail    string         `json:"thumbnail"`
	Description  string         `json:"description"`
	Tags         []string       `json:"tags"`
	UploadDate   string         `json:"upload_date"` // "YYYYMMDD" in UTC
	Timestamp    int64          `json:"timestamp"`   // Unix seconds; used to recover local-timezone date
	Comments     []ytdlpComment `json:"comments"`    // only populated with --write-comments
}

type ytdlpComment struct {
	Text   string `json:"text"`
	Author string `json:"author"`
}

// ytdlpFetch runs yt-dlp against the given URL and returns videos tagged
// with the given platform name. Works for any public URL yt-dlp supports.
// When withComments is true, --write-comments is added to fetch comment text
// (slower; YouTube limited to 20 comments via extractor-args).
func ytdlpFetch(platform, url string, withComments bool) ([]internal.Video, error) {
	args := []string{"--dump-json", "--quiet", "--no-warnings"}
	if withComments {
		args = append(args, "--write-comments")
		if platform == "youtube" {
			args = append(args, "--extractor-args", "youtube:max_comments=20,all,none,none")
		}
	}
	args = append(args, url)

	cmd := exec.Command("yt-dlp", args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: pipe: %w", platform, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: yt-dlp not found — install with: brew install yt-dlp\n%w", platform, err)
	}

	var videos []internal.Video
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

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
		if v.Timestamp > 0 {
			// Use local timezone so evening uploads don't roll to next UTC day.
			// This matches what YouTube shows (creator's local date).
			t := time.Unix(v.Timestamp, 0)
			publishedAt = t.Format("2006-01-02") + "T00:00:00Z"
		} else if len(v.UploadDate) == 8 {
			publishedAt = fmt.Sprintf("%s-%s-%sT00:00:00Z",
				v.UploadDate[0:4], v.UploadDate[4:6], v.UploadDate[6:8])
		}

		videos = append(videos, internal.Video{
			Platform:        platform,
			ID:              v.ID,
			Title:           v.Title,
			Views:           v.ViewCount,
			Likes:           v.LikeCount,
			Comments:        v.CommentCount,
			Shares:          v.RepostCount,
			CommentTexts:    extractCommentTexts(v.Comments, 20),
			Thumbnail:       v.Thumbnail,
			Description:     v.Description,
			Tags:            v.Tags,
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

// extractCommentTexts formats the first n yt-dlp comments as "Author: text" strings.
func extractCommentTexts(comments []ytdlpComment, n int) []string {
	if len(comments) == 0 {
		return nil
	}
	if len(comments) > n {
		comments = comments[:n]
	}
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Author != "" {
			out = append(out, c.Author+": "+text)
		} else {
			out = append(out, text)
		}
	}
	return out
}
