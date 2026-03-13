package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
func SaveReport(groups []VideoGroup, unmatched []UnmatchedVideo, dryRun bool) error {
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
		return fmt.Errorf("marshal report: %w", err)
	}

	if dryRun {
		fmt.Println(string(reportJSON))
		return nil
	}

	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return fmt.Errorf("create reports dir: %w", err)
	}

	reportPath := filepath.Join(reportsDir, fileName)
	if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	// JS wrapper lets the dashboard load via file:// without a local server
	jsPath := filepath.Join(reportsDir, reportID+".js")
	jsContent := fmt.Sprintf("window.__devrelReport=%s;", string(reportJSON))
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		return fmt.Errorf("write report js: %w", err)
	}
	fmt.Printf("Wrote report: %s\n", reportPath)

	if err := updateIndex(reportID, fileName, now); err != nil {
		return fmt.Errorf("update index: %w", err)
	}

	if err := gitCommitAndPush(reportID); err != nil {
		return fmt.Errorf("git: %w", err)
	}

	return nil
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
		File:        fileName,
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
	prev := index.Reports[0] // most recent is first
	reportPath := filepath.Join(reportsDir, prev.File)
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("read previous report %s: %w", prev.File, err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		return nil, fmt.Errorf("parse previous report: %w", err)
	}
	return &report, nil
}

// BackfillMissingTikTokVideos guards against yt-dlp returning an incomplete
// video list on a given run. It checks the previous report for TikTok videos
// absent from current and re-adds them with their last-known view counts.
//
// Backfill is skipped entirely when current is empty (full fetch failure).
// Returns the extended slice and the count of backfilled videos.
func BackfillMissingTikTokVideos(current []Video, prev *Report) ([]Video, int) {
	if len(current) == 0 || prev == nil {
		return current, 0
	}

	// Index TikTok IDs already in this run
	have := make(map[string]bool, len(current))
	for _, v := range current {
		if v.Platform == "tiktok" {
			have[v.ID] = true
		}
	}

	var n int

	// Recover from video_groups
	for _, g := range prev.VideoGroups {
		pd, ok := g.Platforms["tiktok"]
		if !ok || have[pd.VideoID] {
			continue
		}
		current = append(current, Video{
			Platform:        "tiktok",
			ID:              pd.VideoID,
			Title:           pd.Title,
			Views:           pd.Views,
			DurationSeconds: pd.DurationSeconds,
			URL:             pd.URL,
			PublishedAt:     pd.PublishedAt,
		})
		have[pd.VideoID] = true
		n++
	}

	// Recover from unmatched
	for _, u := range prev.Unmatched {
		if u.Platform != "tiktok" || have[u.VideoID] {
			continue
		}
		current = append(current, Video{
			Platform:        "tiktok",
			ID:              u.VideoID,
			Title:           u.Title,
			Views:           u.Views,
			DurationSeconds: u.DurationSeconds,
			URL:             u.URL,
			PublishedAt:     u.PublishedAt,
		})
		have[u.VideoID] = true
		n++
	}

	return current, n
}

func gitCommitAndPush(reportID string) error {
	// Verify we're in a git repo
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		fmt.Println("Warning: not a git repository; skipping commit/push")
		return nil
	}

	cmds := [][]string{
		{"git", "add", "reports/"},
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
