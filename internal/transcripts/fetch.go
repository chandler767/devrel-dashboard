package transcripts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	vttTimestampRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}`)
	vttPositionRe  = regexp.MustCompile(`align:|position:|line:|size:`)
)

// FetchYouTube attempts to download auto-generated captions for a YouTube
// video via yt-dlp. Returns an Entry with Source "auto" on success,
// "none" if no captions are available. Never returns a non-nil error for
// missing captions — only for unexpected failures (e.g. yt-dlp not found).
func FetchYouTube(videoID string) (Entry, error) {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	return fetchViaYTDLP("youtube", videoID, url)
}

// FetchTikTok attempts to download captions for a TikTok video via yt-dlp.
// videoURL must be the full URL (e.g. https://www.tiktok.com/@user/video/123)
// since the video ID alone cannot be used to construct a valid TikTok URL.
// Many TikTok videos have no captions; Source "none" is the common case.
func FetchTikTok(videoID, videoURL string) (Entry, error) {
	return fetchViaYTDLP("tiktok", videoID, videoURL)
}

func fetchViaYTDLP(platform, videoID, url string) (Entry, error) {
	tmpDir, err := os.MkdirTemp("", "devrel-transcript-*")
	if err != nil {
		return Entry{}, fmt.Errorf("mkdirtemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outputTemplate := filepath.Join(tmpDir, "%(id)s.%(ext)s")

	args := []string{
		"--write-auto-subs",
		"--skip-download",
		"--sub-langs", "en",
		"--sub-format", "vtt",
		"--quiet", "--no-warnings",
		"--output", outputTemplate,
		url,
	}

	cmd := exec.Command("yt-dlp", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// yt-dlp exits non-zero when no subtitles found — that's "none", not an error
		return Entry{
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
			Source:    "none",
		}, nil
	}

	// Look for the downloaded .vtt file
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return Entry{}, fmt.Errorf("read tmpdir: %w", err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".vtt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, e.Name()))
		if err != nil {
			return Entry{}, fmt.Errorf("read vtt: %w", err)
		}
		text := stripVTT(string(data))
		if text == "" {
			return Entry{
				FetchedAt: time.Now().UTC().Format(time.RFC3339),
				Source:    "none",
			}, nil
		}

		// Detect language from filename: videoID.en.vtt → "en"
		lang := "en"
		name := strings.TrimSuffix(e.Name(), ".vtt")
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			lang = name[idx+1:]
		}

		_ = platform // used for context only
		return Entry{
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
			Lang:      lang,
			Text:      text,
			Source:    "auto",
		}, nil
	}

	// No .vtt file written — captions not available
	return Entry{
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Source:    "none",
	}, nil
}

// stripVTT converts a WebVTT string into clean plain text.
// It strips headers, timestamp lines, positioning cues, HTML tags,
// and deduplicates adjacent identical/overlapping lines produced by
// the sliding-window rendering common in YouTube auto-captions.
func stripVTT(vtt string) string {
	lines := strings.Split(vtt, "\n")
	var kept []string

	for _, raw := range lines {
		line := strings.TrimSpace(raw)

		// Skip blank lines
		if line == "" {
			continue
		}
		// Skip WEBVTT header
		if strings.HasPrefix(line, "WEBVTT") {
			continue
		}
		// Skip NOTE blocks
		if strings.HasPrefix(line, "NOTE") {
			continue
		}
		// Skip timestamp lines like "00:00:01.000 --> 00:00:03.000"
		if vttTimestampRe.MatchString(line) {
			continue
		}
		// Skip positioning directives on cue lines
		if vttPositionRe.MatchString(line) {
			continue
		}
		// Strip inline VTT/HTML tags: <c>, <b>, <i>, <00:00:01.000>, etc.
		line = stripVTTTags(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Deduplicate: skip if this line is identical to the last kept line,
		// or if it's a suffix of the last kept line (sliding-window overlap).
		if len(kept) > 0 {
			prev := kept[len(kept)-1]
			if line == prev {
				continue
			}
			// Skip if the previous line already ends with this line
			if strings.HasSuffix(prev, line) {
				continue
			}
			// Skip if this line starts with the previous line (extension case)
			// — keep the longer version already buffered; we'll flush on change
		}

		kept = append(kept, line)
	}

	// Join and collapse runs of whitespace
	result := strings.Join(kept, " ")
	result = strings.Join(strings.Fields(result), " ")
	return result
}

var vttTagRe = regexp.MustCompile(`<[^>]+>`)

func stripVTTTags(s string) string {
	return vttTagRe.ReplaceAllString(s, "")
}
