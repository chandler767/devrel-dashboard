package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"github.com/devrel-dashboard/internal"
	"github.com/devrel-dashboard/internal/platforms"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Print the report JSON to stdout without writing files or committing")
	skipYT := flag.Bool("skip-youtube", false, "Skip fetching from YouTube")
	skipTT := flag.Bool("skip-tiktok", false, "Skip fetching from TikTok")
	skipLI := flag.Bool("skip-linkedin", false, "Skip fetching from LinkedIn")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: .env file not found, using environment variables")
	}

	prevReport, err := internal.LoadPreviousReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load previous report: %v\n", err)
	}

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
