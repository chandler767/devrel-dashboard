package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/term"

	"github.com/devrel-dashboard/internal"
	"github.com/devrel-dashboard/internal/analysis"
	"github.com/devrel-dashboard/internal/platforms"
	"github.com/devrel-dashboard/internal/transcripts"
)

func main() {
	dryRun         := flag.Bool("dry-run", false, "Print the report JSON to stdout without writing files or committing")
	skipYT         := flag.Bool("skip-youtube", false, "Skip fetching from YouTube")
	skipTT         := flag.Bool("skip-tiktok", false, "Skip fetching from TikTok")
	skipLI         := flag.Bool("skip-linkedin", false, "Skip fetching from LinkedIn")
	liAuth         := flag.Bool("linkedin-auth", false, "Run one-time LinkedIn OAuth setup and exit")
	since          := flag.String("since", "", "Only include new videos published on or after this date (YYYY-MM-DD). Already-approved videos are unaffected.")
	fetchComments  := flag.Bool("fetch-comments", true, "Fetch comment text for each video (slower; ~20 comments per video)")
	keepDuplicates := flag.Bool("keep-duplicates", false, "Keep existing reports from the same AM/PM window instead of replacing them")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: .env file not found, using environment variables")
	}

	if *liAuth {
		if err := platforms.LinkedInAuth(); err != nil {
			fmt.Fprintf(os.Stderr, "LinkedIn auth error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	var sinceTime time.Time
	if *since != "" {
		var err error
		sinceTime, err = time.Parse("2006-01-02", *since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid --since date %q: use YYYY-MM-DD format\n", *since)
			os.Exit(1)
		}
	}

	prevReport, err := internal.LoadPreviousReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load previous report: %v\n", err)
	}

	known    := internal.LoadAllKnownVideoIDs()
	rejected := internal.LoadRejectedVideoIDs()

	// ── Concurrent platform fetches ───────────────────────────────────────────

	type fetchResult struct {
		videos []internal.Video
		err    error
	}

	type job struct {
		name string
		fn   func() ([]internal.Video, error)
	}

	var jobs []job
	if !*skipYT {
		wc := *fetchComments
		jobs = append(jobs, job{"youtube", func() ([]internal.Video, error) { return platforms.YouTubeFetch(wc) }})
	}
	if !*skipTT {
		wc := *fetchComments
		jobs = append(jobs, job{"tiktok", func() ([]internal.Video, error) { return platforms.TikTokFetch(wc) }})
	}
	if !*skipLI {
		jobs = append(jobs, job{"linkedin", func() ([]internal.Video, error) { return platforms.LinkedInFetch() }})
	}

	results := make(map[string]fetchResult, len(jobs))
	if len(jobs) > 0 {
		fmt.Printf("Fetching from %d platform(s) concurrently...\n", len(jobs))
		type tagged struct {
			name string
			fetchResult
		}
		ch := make(chan tagged, len(jobs))
		var wg sync.WaitGroup
		for _, j := range jobs {
			j := j
			wg.Add(1)
			go func() {
				defer wg.Done()
				fmt.Printf("Fetching %s...\n", j.name)
				v, e := j.fn()
				ch <- tagged{j.name, fetchResult{v, e}}
			}()
		}
		wg.Wait()
		close(ch)
		for r := range ch {
			results[r.name] = r.fetchResult
		}
	}

	var allVideos []internal.Video

	if r, ok := results["youtube"]; ok {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "YouTube error: %v\n", r.err)
			if carried := carryForwardSkipped([]string{"youtube"}, prevReport); len(carried) > 0 {
				fmt.Printf("  Carried forward %d YouTube video(s) from previous report (fetch failed)\n", len(carried))
				allVideos = append(allVideos, carried...)
			}
		} else {
			videos, backfilled := internal.BackfillMissingVideos("youtube", r.videos, prevReport)
			fmt.Printf("YouTube: %d video(s)", len(r.videos))
			if backfilled > 0 {
				fmt.Printf(" (+%d backfilled)", backfilled)
			}
			fmt.Println()
			allVideos = append(allVideos, videos...)
		}
	}

	if r, ok := results["tiktok"]; ok {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "TikTok error: %v\n", r.err)
			if carried := carryForwardSkipped([]string{"tiktok"}, prevReport); len(carried) > 0 {
				fmt.Printf("  Carried forward %d TikTok video(s) from previous report (fetch failed)\n", len(carried))
				allVideos = append(allVideos, carried...)
			}
		} else {
			videos, backfilled := internal.BackfillMissingVideos("tiktok", r.videos, prevReport)
			fmt.Printf("TikTok: %d video(s)", len(r.videos))
			if backfilled > 0 {
				fmt.Printf(" (+%d backfilled)", backfilled)
			}
			fmt.Println()
			allVideos = append(allVideos, videos...)
		}
	}

	if r, ok := results["linkedin"]; ok {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "LinkedIn error: %v\n", r.err)
			if carried := carryForwardSkipped([]string{"linkedin"}, prevReport); len(carried) > 0 {
				fmt.Printf("  Carried forward %d LinkedIn video(s) from previous report (fetch failed)\n", len(carried))
				allVideos = append(allVideos, carried...)
			}
		} else if len(r.videos) == 0 {
			fmt.Println("LinkedIn: 0 videos (likely rate-limited) — carrying forward from previous report")
			if carried := carryForwardSkipped([]string{"linkedin"}, prevReport); len(carried) > 0 {
				fmt.Printf("  Carried forward %d LinkedIn video(s)\n", len(carried))
				allVideos = append(allVideos, carried...)
			}
		} else {
			videos, backfilled := internal.BackfillMissingVideos("linkedin", r.videos, prevReport)
			fmt.Printf("LinkedIn: %d video(s)", len(r.videos))
			if backfilled > 0 {
				fmt.Printf(" (+%d backfilled)", backfilled)
			}
			fmt.Println()
			allVideos = append(allVideos, videos...)
		}
	}

	fmt.Println("Fetching manual videos (manual_videos.json)...")
	if manualVideos, err := platforms.ManualFetch(); err != nil {
		fmt.Fprintf(os.Stderr, "Manual videos error: %v\n", err)
	} else if len(manualVideos) > 0 {
		fmt.Printf("  Found %d manual videos\n", len(manualVideos))
		allVideos = append(allVideos, manualVideos...)
	}

	// Carry forward videos for any skipped platforms from the previous report
	var skipped []string
	if *skipYT {
		skipped = append(skipped, "youtube")
	}
	if *skipTT {
		skipped = append(skipped, "tiktok")
	}
	if *skipLI {
		skipped = append(skipped, "linkedin")
	}
	if len(skipped) > 0 {
		carried := carryForwardSkipped(skipped, prevReport)
		if len(carried) > 0 {
			fmt.Printf("  Carried forward %d video(s) from previous report for skipped platform(s): %s\n", len(carried), strings.Join(skipped, ", "))
			allVideos = append(allVideos, carried...)
		}
	}

	// Drop new videos older than --since (already-approved videos are unaffected)
	if !sinceTime.IsZero() {
		allVideos = filterSince(allVideos, sinceTime, known)
	}

	// Interactive approval for new videos (skipped in dry-run mode)
	if !*dryRun {
		allVideos = approveNewVideos(allVideos, known, rejected)
	}

	fmt.Printf("\nGrouping %d videos across platforms...\n", len(allVideos))
	groups, unmatched := internal.Group(allVideos)
	fmt.Printf("  %d video groups, %d unmatched\n\n", len(groups), len(unmatched))

	reportID, err := internal.SaveReport(groups, unmatched, *dryRun, *keepDuplicates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving report: %v\n", err)
		os.Exit(1)
	}

	if !*dryRun {
		store := autoFetchNewTranscripts(groups, unmatched)

		if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
			fmt.Println("Generating content analysis...")
			transcriptTexts := flattenTranscripts(store)
			report := &internal.Report{
				ReportID:    reportID,
				VideoGroups: groups,
				Unmatched:   unmatched,
			}
			result, err := analysis.Generate(report, transcriptTexts, apiKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: content analysis failed: %v\n", err)
			} else if err := analysis.Save(result); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save analysis: %v\n", err)
			} else {
				fmt.Printf("Content analysis saved (%d chars)\n", len(result.Text))
			}
		} else {
			fmt.Println("(Skipping content analysis — set ANTHROPIC_API_KEY in .env to enable)")
		}

		if err := internal.GitCommitAndPush(reportID); err != nil {
			fmt.Fprintf(os.Stderr, "Error pushing to git: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Done! Report saved and pushed to GitHub.")
	}
}

// carryForwardSkipped extracts videos for the given platforms from the previous
// report so they are not lost when a platform is skipped on a run.
func carryForwardSkipped(skipped []string, prev *internal.Report) []internal.Video {
	if prev == nil {
		return nil
	}
	skip := make(map[string]bool, len(skipped))
	for _, p := range skipped {
		skip[p] = true
	}
	seen := map[string]bool{}
	var out []internal.Video
	for _, g := range prev.VideoGroups {
		for platform, pd := range g.Platforms {
			if !skip[platform] {
				continue
			}
			key := platform + ":" + pd.VideoID
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, internal.Video{
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
		}
	}
	for _, u := range prev.Unmatched {
		if !skip[u.Platform] {
			continue
		}
		key := u.Platform + ":" + u.VideoID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, internal.Video{
			Platform:        u.Platform,
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
	}
	return out
}

// filterSince drops new videos published before cutoff.
// Videos already approved in the previous report pass through unchanged.
func filterSince(videos []internal.Video, cutoff time.Time, known map[string]bool) []internal.Video {
	var out []internal.Video
	dropped := 0
	for _, v := range videos {
		if known[v.Platform+":"+v.ID] {
			out = append(out, v)
			continue
		}
		t, err := time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil || !t.Before(cutoff) {
			out = append(out, v)
		} else {
			dropped++
		}
	}
	if dropped > 0 {
		fmt.Printf("  Dropped %d video(s) published before %s (--since)\n", dropped, cutoff.Format("2006-01-02"))
	}
	return out
}

// approveNewVideos prompts for each video not seen in the previous report.
// Known videos pass through automatically without prompting.
// Reads a single keypress (no Enter required): y=include, n=skip.
func approveNewVideos(videos []internal.Video, known, rejected map[string]bool) []internal.Video {
	var out []internal.Video
	newCount := 0
	for _, v := range videos {
		key := v.Platform + ":" + v.ID
		if known[key] {
			out = append(out, v)
			continue
		}
		if rejected[key] {
			continue // silently skip previously rejected videos
		}
		newCount++
		fmt.Printf("\n  New %s video:\n", v.Platform)
		if v.Author != "" {
			fmt.Printf("    Author:    %s\n", v.Author)
		}
		fmt.Printf("    Title:     %s\n", v.Title)
		fmt.Printf("    Published: %s\n", v.PublishedAt)
		fmt.Printf("    Views:     %d\n", v.Views)
		fmt.Printf("    URL:       %s\n", v.URL)
		fmt.Print("  Include? [y/n] ")

		ch := readKey()
		if ch == "\x03" { // Ctrl+C in raw mode
			fmt.Println("\nAborted.")
			os.Exit(130)
		}
		fmt.Println(ch) // echo the key
		if strings.ToLower(ch) == "y" {
			out = append(out, v)
		} else {
			if err := internal.SaveRejectedVideoID(v.Platform, v.ID); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not save rejection: %v\n", err)
			}
		}
	}
	if newCount == 0 {
		fmt.Println("  No new videos to approve.")
	}
	return out
}

// autoFetchNewTranscripts fetches transcripts for any YouTube/TikTok videos
// that are not yet in the transcript store. Called automatically after a
// successful report save so new approved videos get transcripts on first run.
func autoFetchNewTranscripts(groups []internal.VideoGroup, unmatched []internal.UnmatchedVideo) *transcripts.Store {
	store, err := transcripts.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load transcript store: %v\n", err)
		return store
	}

	// Collect new video refs (key + URL) not yet in the store
	type ref struct{ key, url string }
	seen := map[string]bool{}
	var newRefs []ref

	add := func(platform, videoID, url string) {
		if platform == "linkedin" || platform == "tiktok" {
			return
		}
		k := platform + ":" + videoID
		if !seen[k] && !store.Has(k) {
			seen[k] = true
			newRefs = append(newRefs, ref{k, url})
		}
	}

	for _, g := range groups {
		for platform, pd := range g.Platforms {
			add(platform, pd.VideoID, pd.URL)
		}
	}
	for _, u := range unmatched {
		add(u.Platform, u.VideoID, u.URL)
	}

	if len(newRefs) == 0 {
		return store
	}

	fmt.Printf("Fetching transcripts for %d new video(s)...\n", len(newRefs))
	var saved int
	for _, r := range newRefs {
		key := r.key
		parts := splitKey(key)
		platform, videoID := parts[0], parts[1]

		var entry transcripts.Entry
		switch platform {
		case "youtube":
			entry, err = transcripts.FetchYouTube(videoID)
		case "tiktok":
			if r.url == "" {
				fmt.Printf("  skip %s (no URL)\n", key)
				continue
			}
			entry, err = transcripts.FetchTikTok(videoID, r.url)
		default:
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  transcript %s: %v\n", key, err)
			continue
		}
		store.Set(key, entry)
		saved++
		if entry.Source == "auto" || entry.Source == "manual" {
			fmt.Printf("  ✓ %s (%d chars)\n", key, len(entry.Text))
		} else {
			fmt.Printf("  - %s (no captions)\n", key)
		}
	}

	if saved > 0 {
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save transcript store: %v\n", err)
		}
	}
	return store
}

// flattenTranscripts converts a transcript store into a plain map of
// "platform:videoId" → text for use in the analysis prompt.
func flattenTranscripts(store *transcripts.Store) map[string]string {
	if store == nil {
		return nil
	}
	out := make(map[string]string, len(store.Transcripts))
	for key, entry := range store.Transcripts {
		if entry.Text != "" {
			out[key] = entry.Text
		}
	}
	return out
}

func splitKey(key string) [2]string {
	idx := len("youtube") // shortest platform name length as starting guess
	for i, c := range key {
		if c == ':' {
			idx = i
			break
		}
	}
	return [2]string{key[:idx], key[idx+1:]}
}

// readKey reads a single keypress from stdin without requiring Enter.
// Falls back to a full line read if stdin is not a terminal.
func readKey() string {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		old, err := term.MakeRaw(fd)
		if err == nil {
			defer term.Restore(fd, old)
			buf := make([]byte, 1)
			if _, err := os.Stdin.Read(buf); err == nil {
				return string(buf)
			}
		}
	}
	// Non-terminal fallback (pipes, tests)
	var line string
	fmt.Scanln(&line)
	return strings.TrimSpace(line)
}
