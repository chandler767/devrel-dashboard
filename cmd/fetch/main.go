package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/term"

	"github.com/devrel-dashboard/internal"
	"github.com/devrel-dashboard/internal/platforms"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Print the report JSON to stdout without writing files or committing")
	skipYT := flag.Bool("skip-youtube", false, "Skip fetching from YouTube")
	skipTT := flag.Bool("skip-tiktok", false, "Skip fetching from TikTok")
	skipLI := flag.Bool("skip-linkedin", false, "Skip fetching from LinkedIn")
	liAuth := flag.Bool("linkedin-auth", false, "Run one-time LinkedIn OAuth setup and exit")
	since  := flag.String("since", "", "Only include new videos published on or after this date (YYYY-MM-DD). Already-approved videos are unaffected.")
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

	known := internal.LoadAllKnownVideoIDs()

	var allVideos []internal.Video

	if !*skipYT {
		fmt.Println("Fetching YouTube videos...")
		videos, err := platforms.YouTubeFetch()
		if err != nil {
			fmt.Fprintf(os.Stderr, "YouTube error: %v\n", err)
		} else {
			fmt.Printf("  Found %d YouTube videos\n", len(videos))
			allVideos = append(allVideos, videos...)
		}
	}

	if !*skipTT {
		fmt.Println("Fetching TikTok videos...")
		videos, err := platforms.TikTokFetch()
		if err != nil {
			fmt.Fprintf(os.Stderr, "TikTok error: %v\n", err)
		} else {
			fmt.Printf("  Found %d TikTok videos\n", len(videos))
			videos, backfilled := internal.BackfillMissingTikTokVideos(videos, prevReport)
			if backfilled > 0 {
				fmt.Printf("  Backfilled %d TikTok video(s) from previous report (yt-dlp miss)\n", backfilled)
			}
			allVideos = append(allVideos, videos...)
		}
	}

	if !*skipLI {
		fmt.Println("Fetching LinkedIn videos...")
		videos, err := platforms.LinkedInFetch()
		if err != nil {
			fmt.Fprintf(os.Stderr, "LinkedIn error: %v\n", err)
		} else {
			fmt.Printf("  Found %d LinkedIn videos\n", len(videos))
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
		allVideos = approveNewVideos(allVideos, known)
	}

	fmt.Printf("\nGrouping %d videos across platforms...\n", len(allVideos))
	groups, unmatched := internal.Group(allVideos)
	fmt.Printf("  %d video groups, %d unmatched\n\n", len(groups), len(unmatched))

	if err := internal.SaveReport(groups, unmatched, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving report: %v\n", err)
		os.Exit(1)
	}

	if !*dryRun {
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
func approveNewVideos(videos []internal.Video, known map[string]bool) []internal.Video {
	var out []internal.Video
	newCount := 0
	for _, v := range videos {
		if known[v.Platform+":"+v.ID] {
			out = append(out, v)
			continue
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
		fmt.Println(ch) // echo the key
		if strings.ToLower(ch) == "y" {
			out = append(out, v)
		}
	}
	if newCount == 0 {
		fmt.Println("  No new videos to approve.")
	}
	return out
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
