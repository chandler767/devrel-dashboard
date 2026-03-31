package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devrel-dashboard/internal"
)

const (
	analysisDir    = "analysis"
	defaultModel   = "claude-haiku-4-5-20251001"
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"
)

// Result is stored as analysis/<reportID>.json.
type Result struct {
	ReportID    string `json:"report_id"`
	GeneratedAt string `json:"generated_at"`
	Model       string `json:"model"`
	VideoCount  int    `json:"video_count"`
	Text        string `json:"text"` // raw markdown with ### section headings
}

// Generate calls the Anthropic API to analyze the videos in a report.
// transcriptTexts is a map of "platform:videoId" → transcript text.
func Generate(report *internal.Report, transcriptTexts map[string]string, apiKey string) (*Result, error) {
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultModel
	}

	allGroups := report.VideoGroups
	unmatched := report.Unmatched
	videoCount := len(allGroups) + len(unmatched)

	prompt := buildPrompt(allGroups, unmatched, transcriptTexts)

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 2000,
		"system":     "You are a content strategist helping a developer relations team improve their short-form video performance. Analyze the provided video data and give specific, actionable insights. Be concise and direct.",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", anthropicAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call: %w", err)
	}
	defer resp.Body.Close()

	var apiResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if apiResp.Error != nil {
		return nil, fmt.Errorf("Anthropic API error: %s", apiResp.Error.Message)
	}
	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty response from Anthropic API")
	}

	return &Result{
		ReportID:    report.ReportID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Model:       model,
		VideoCount:  videoCount,
		Text:        apiResp.Content[0].Text,
	}, nil
}

// Save writes the result to analysis/<reportID>.json and a JS wrapper.
func Save(result *Result) error {
	if err := os.MkdirAll(analysisDir, 0755); err != nil {
		return fmt.Errorf("create analysis dir: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal analysis: %w", err)
	}

	jsonPath := filepath.Join(analysisDir, result.ReportID+".json")
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}

	jsContent := fmt.Sprintf("window.__devrelAnalysis=%s;", string(data))
	jsPath := filepath.Join(analysisDir, result.ReportID+".js")
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		return fmt.Errorf("write analysis js: %w", err)
	}

	return nil
}

func buildPrompt(groups []internal.VideoGroup, unmatched []internal.UnmatchedVideo, transcripts map[string]string) string {
	type videoEntry struct {
		title       string
		publishedAt string
		views       int64
		likes       int64
		comments    int64
		platforms   []string
		tags        []string
		description string
		commentTexts []string
		transcript  string
	}

	var entries []videoEntry

	for _, g := range groups {
		var platforms []string
		var commentTexts []string
		var transcript string
		for platform, pd := range g.Platforms {
			platforms = append(platforms, platform)
			commentTexts = append(commentTexts, pd.CommentTexts...)
			if t := transcripts[platform+":"+pd.VideoID]; t != "" && transcript == "" {
				transcript = t
			}
		}
		publishedDates := make([]string, 0, len(g.Platforms))
		for _, pd := range g.Platforms {
			if pd.PublishedAt != "" {
				publishedDates = append(publishedDates, pd.PublishedAt)
			}
		}
		publishedAt := ""
		if len(publishedDates) > 0 {
			publishedAt = publishedDates[0][:10]
		}
		entries = append(entries, videoEntry{
			title:        g.CanonicalTitle,
			publishedAt:  publishedAt,
			views:        g.TotalViews,
			likes:        g.TotalLikes,
			comments:     g.TotalComments,
			platforms:    platforms,
			tags:         g.Tags,
			description:  g.Description,
			commentTexts: commentTexts,
			transcript:   transcript,
		})
	}

	for _, v := range unmatched {
		transcript := transcripts[v.Platform+":"+v.VideoID]
		publishedAt := ""
		if len(v.PublishedAt) >= 10 {
			publishedAt = v.PublishedAt[:10]
		}
		entries = append(entries, videoEntry{
			title:        v.Title,
			publishedAt:  publishedAt,
			views:        v.Views,
			likes:        v.Likes,
			comments:     v.Comments,
			platforms:    []string{v.Platform},
			tags:         v.Tags,
			description:  v.Description,
			commentTexts: v.CommentTexts,
			transcript:   transcript,
		})
	}

	// Cap at 50 most-viewed
	if len(entries) > 50 {
		entries = entries[:50]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "I have %d short-form DevRel videos. Please analyze them and provide insights in exactly these four sections:\n\n", len(entries))
	sb.WriteString("### Top Topics & Themes\nList the most common subjects across these videos. Note which topics correlate with higher view counts.\n\n")
	sb.WriteString("### Title & Hook Patterns\nIdentify specific words, phrases, or structural patterns in titles that correlate with better performance. What makes a good hook for this audience?\n\n")
	sb.WriteString("### Viewer Questions & Requests\nExtract recurring questions, requests, or pain points from the comments. Group by theme.\n\n")
	sb.WriteString("### Actionable Recommendations\nGive 5 specific, concrete suggestions for improving future video performance (titles, scripts, topics, posting strategy, etc.).\n\n")
	sb.WriteString("---\nVIDEO DATA:\n\n")

	for _, e := range entries {
		tags := strings.Join(e.tags, ", ")
		desc := e.description
		if len(desc) > 200 {
			desc = desc[:200]
		}
		transcript := e.transcript
		if len(transcript) > 800 {
			transcript = transcript[:800]
		}
		comments := strings.Join(firstN(e.commentTexts, 3), " | ")

		fmt.Fprintf(&sb, "%q | %s | Views: %d | Likes: %d | Comments: %d\n", e.title, e.publishedAt, e.views, e.likes, e.comments)
		fmt.Fprintf(&sb, "Platforms: %s", strings.Join(e.platforms, ", "))
		if tags != "" {
			fmt.Fprintf(&sb, " | Tags: %s", tags)
		}
		sb.WriteString("\n")
		if desc != "" {
			fmt.Fprintf(&sb, "Description: %s\n", desc)
		}
		if transcript != "" {
			fmt.Fprintf(&sb, "Transcript: %s\n", transcript)
		} else {
			sb.WriteString("Transcript: none\n")
		}
		if comments != "" {
			fmt.Fprintf(&sb, "Top comments: %s\n", comments)
		}
		sb.WriteString("---\n")
	}

	return sb.String()
}

func firstN(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return ss[:n]
}
