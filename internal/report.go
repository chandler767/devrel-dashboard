package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var assetVersionRe = regexp.MustCompile(`(dashboard\.(js|css))(?:\?v=[^"' >]+)?`)

const reportsDir = "reports"

// Report is the full structure written to each timestamped JSON file.
type Report struct {
	ReportID    string         `json:"report_id"`
	GeneratedAt string         `json:"generated_at"`
	VideoGroups []VideoGroup   `json:"video_groups"`
	Unmatched   []UnmatchedVideo `json:"unmatched"`
}

// ReportIndexEntry is one entry in reports/index.json.
type ReportIndexEntry struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	GeneratedAt string `json:"generated_at"`
}

// ReportIndex is the full reports/index.json structure.
type ReportIndex struct {
	Reports []ReportIndexEntry `json:"reports"`
}

// SaveReport writes the report to reports/<id>.json, updates reports/index.json,
// and if dryRun is false, commits and pushes to git.
// Returns the generated reportID so callers can associate follow-up files with it.
func SaveReport(groups []VideoGroup, unmatched []UnmatchedVideo, dryRun bool) (string, error) {
	now := time.Now().UTC()
	reportID := now.Format("2006-01-02T15-04-05Z")
	fileName := reportID + ".json"

	report := Report{
		ReportID:    reportID,
		GeneratedAt: now.Format(time.RFC3339),
		VideoGroups: groups,
		Unmatched:   unmatched,
	}

	// Sort groups by total views descending
	sort.Slice(report.VideoGroups, func(i, j int) bool {
		return report.VideoGroups[i].TotalViews > report.VideoGroups[j].TotalViews
	})

	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}

	if dryRun {
		fmt.Println(string(reportJSON))
		return reportID, nil
	}

	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return "", fmt.Errorf("create reports dir: %w", err)
	}

	reportPath := filepath.Join(reportsDir, fileName)
	if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	// JS wrapper lets the dashboard load via file:// without a local server
	jsPath := filepath.Join(reportsDir, reportID+".js")
	jsContent := fmt.Sprintf("window.__devrelReport=%s;", string(reportJSON))
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		return "", fmt.Errorf("write report js: %w", err)
	}
	fmt.Printf("Wrote report: %s\n", reportPath)

	if err := updateIndex(reportID, fileName, now); err != nil {
		return "", fmt.Errorf("update index: %w", err)
	}

	return reportID, nil
}

func updateIndex(reportID, fileName string, generatedAt time.Time) error {
	indexPath := filepath.Join(reportsDir, "index.json")

	var index ReportIndex

	data, err := os.ReadFile(indexPath)
	if err == nil {
		_ = json.Unmarshal(data, &index)
	}

	newEntry := ReportIndexEntry{
		ID:          reportID,
		File:        filepath.Join(reportsDir, fileName),
		GeneratedAt: generatedAt.Format(time.RFC3339),
	}

	// Prepend new entry (most recent first)
	index.Reports = append([]ReportIndexEntry{newEntry}, index.Reports...)

	// Deduplicate by ID
	seen := map[string]bool{}
	deduped := index.Reports[:0]
	for _, e := range index.Reports {
		if !seen[e.ID] {
			seen[e.ID] = true
			deduped = append(deduped, e)
		}
	}
	index.Reports = deduped

	out, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(indexPath, out, 0644); err != nil {
		return err
	}
	// JS wrapper for file:// support
	indexJSPath := filepath.Join(reportsDir, "index.js")
	indexJS := fmt.Sprintf("window.__devrelIndex=%s;", string(out))
	return os.WriteFile(indexJSPath, []byte(indexJS), 0644)
}

// LoadAllKnownVideoIDs scans every report file listed in reports/index.json
// and returns the union of all "platform:videoID" pairs ever approved.
func LoadAllKnownVideoIDs() map[string]bool {
	known := map[string]bool{}

	indexPath := filepath.Join(reportsDir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return known
	}
	var index ReportIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return known
	}

	for _, entry := range index.Reports {
		reportData, err := os.ReadFile(entry.File)
		if err != nil {
			continue // deleted or missing — skip
		}
		var report Report
		if err := json.Unmarshal(reportData, &report); err != nil {
			continue
		}
		for _, g := range report.VideoGroups {
			for platform, pd := range g.Platforms {
				known[platform+":"+pd.VideoID] = true
			}
		}
		for _, u := range report.Unmatched {
			known[u.Platform+":"+u.VideoID] = true
		}
	}

	return known
}

// LoadPreviousReport reads the most recent report from reports/index.json.
// Returns nil (no error) if no previous report exists yet.
func LoadPreviousReport() (*Report, error) {
	indexPath := filepath.Join(reportsDir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, nil // no index yet — first run
	}
	var index ReportIndex
	if err := json.Unmarshal(data, &index); err != nil || len(index.Reports) == 0 {
		return nil, nil
	}
	for _, prev := range index.Reports {
		reportData, err := os.ReadFile(prev.File)
		if os.IsNotExist(err) {
			continue // file was deleted; try the next one
		}
		if err != nil {
			return nil, fmt.Errorf("read previous report %s: %w", prev.File, err)
		}
		var report Report
		if err := json.Unmarshal(reportData, &report); err != nil {
			return nil, fmt.Errorf("parse previous report: %w", err)
		}
		return &report, nil
	}
	return nil, nil // all index entries point to deleted files
}

// BackfillMissingVideos guards against a platform returning an incomplete
// video list on a given run (e.g. yt-dlp pagination gaps, LinkedIn API
// inconsistencies). It re-adds any video for the given platform from the
// previous report that is absent from the current fetch, preserving
// last-known metrics.
//
// Backfill is skipped entirely when current is empty (full fetch failure).
// Returns the extended slice and the count of backfilled videos.
func BackfillMissingVideos(platform string, current []Video, prev *Report) ([]Video, int) {
	if len(current) == 0 || prev == nil {
		return current, 0
	}

	have := make(map[string]bool, len(current))
	for _, v := range current {
		if v.Platform == platform {
			have[v.ID] = true
		}
	}

	var n int

	for _, g := range prev.VideoGroups {
		pd, ok := g.Platforms[platform]
		if !ok || have[pd.VideoID] {
			continue
		}
		current = append(current, Video{
			Platform:        platform,
			ID:              pd.VideoID,
			Title:           pd.Title,
			Views:           pd.Views,
			Likes:           pd.Likes,
			Comments:        pd.Comments,
			Shares:          pd.Shares,
			Clicks:          pd.Clicks,
			CommentTexts:    pd.CommentTexts,
			Thumbnail:       pd.Thumbnail,
			Description:     pd.Description,
			Tags:            pd.Tags,
			DurationSeconds: pd.DurationSeconds,
			URL:             pd.URL,
			PublishedAt:     pd.PublishedAt,
		})
		have[pd.VideoID] = true
		n++
	}

	for _, u := range prev.Unmatched {
		if u.Platform != platform || have[u.VideoID] {
			continue
		}
		current = append(current, Video{
			Platform:        platform,
			ID:              u.VideoID,
			Title:           u.Title,
			Views:           u.Views,
			Likes:           u.Likes,
			Comments:        u.Comments,
			Shares:          u.Shares,
			Clicks:          u.Clicks,
			CommentTexts:    u.CommentTexts,
			Thumbnail:       u.Thumbnail,
			Description:     u.Description,
			Tags:            u.Tags,
			DurationSeconds: u.DurationSeconds,
			URL:             u.URL,
			PublishedAt:     u.PublishedAt,
		})
		have[u.VideoID] = true
		n++
	}

	return current, n
}

// BackfillMissingTikTokVideos is kept for backwards compatibility.
// Prefer BackfillMissingVideos("tiktok", ...).
func BackfillMissingTikTokVideos(current []Video, prev *Report) ([]Video, int) {
	return BackfillMissingVideos("tiktok", current, prev)
}

// updateAssetVersions rewrites index.html so that dashboard.js and
// dashboard.css carry a ?v=<version> query param, busting browser cache
// whenever a new report (and potentially new JS/CSS) is deployed.
func updateAssetVersions(version string) error {
	data, err := os.ReadFile("index.html")
	if err != nil {
		return err
	}
	updated := assetVersionRe.ReplaceAllString(string(data), "${1}?v="+version)
	return os.WriteFile("index.html", []byte(updated), 0644)
}

// GitCommitAndPush stages all report, transcript, and analysis files and
// pushes to the remote. Call this after transcripts and analysis are written.
func GitCommitAndPush(reportID string) error {
	// Verify we're in a git repo
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		fmt.Println("Warning: not a git repository; skipping commit/push")
		return nil
	}

	if err := updateAssetVersions(reportID); err != nil {
		fmt.Printf("Warning: could not update asset versions in index.html: %v\n", err)
	}

	gitAdd := []string{"git", "add", "reports/", "index.html"}
	// Include transcript and analysis files in the commit if they exist
	for _, f := range []string{"transcripts.json", "transcripts.js", "analysis/"} {
		if _, err := os.Stat(f); err == nil {
			gitAdd = append(gitAdd, f)
		}
	}

	cmds := [][]string{
		gitAdd,
		{"git", "commit", "-m", "report: " + reportID},
		{"git", "push"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// If commit fails because there's nothing to commit, that's fine
			if args[0] == "git" && args[1] == "commit" && strings.Contains(err.Error(), "exit status") {
				fmt.Println("Nothing to commit (report unchanged)")
				return nil
			}
			return fmt.Errorf("command %v: %w", args, err)
		}
	}

	return nil
}
