package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/devrel-dashboard/internal"
	"github.com/devrel-dashboard/internal/transcripts"
)

func main() {
	all     := flag.Bool("all", false, "Fetch transcripts for all YouTube/TikTok videos in the latest report")
	video   := flag.String("video", "", "Fetch transcript for a single video (format: platform:videoId, e.g. youtube:abc123)")
	refresh := flag.Bool("refresh", false, "Force re-fetch even if transcript already stored")
	dryRun  := flag.Bool("dry-run", false, "Print what would be fetched without writing any files")
	flag.Parse()

	if !*all && *video == "" {
		fmt.Fprintln(os.Stderr, "Usage: transcripts --all [--refresh] [--dry-run]")
		fmt.Fprintln(os.Stderr, "       transcripts --video youtube:abc123 [--refresh] [--dry-run]")
		os.Exit(1)
	}

	store, err := transcripts.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading transcript store: %v\n", err)
		os.Exit(1)
	}

	if *video != "" {
		// Single-video mode
		if err := processOne(store, *video, *refresh, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if !*dryRun {
			if err := store.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving transcript store: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	// --all mode: collect video IDs from the latest report
	report, err := internal.LoadPreviousReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading latest report: %v\n", err)
		os.Exit(1)
	}
	if report == nil {
		fmt.Fprintln(os.Stderr, "No report found. Run the fetch command first.")
		os.Exit(1)
	}

	keys := collectKeys(report)
	fmt.Printf("Found %d YouTube/TikTok video(s) in latest report.\n", len(keys))

	var toFetch []string
	var skipped int
	for _, k := range keys {
		if store.Has(k) && !*refresh {
			skipped++
			continue
		}
		toFetch = append(toFetch, k)
	}

	if skipped > 0 {
		fmt.Printf("Skipping %d already-stored transcript(s) (use --refresh to re-fetch).\n", skipped)
	}
	if len(toFetch) == 0 {
		fmt.Println("Nothing to fetch.")
		return
	}

	var fetched, failed int
	for _, key := range toFetch {
		if err := processOne(store, key, true, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "  error %s: %v\n", key, err)
			failed++
		} else {
			fetched++
		}
	}

	fmt.Printf("\nSummary: %d fetched, %d skipped, %d failed\n", fetched, skipped, failed)

	if !*dryRun {
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving transcript store: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Saved transcripts.json")
	}
}

// processOne fetches and stores a single transcript by "platform:videoId" key.
func processOne(store *transcripts.Store, key string, force bool, dryRun bool) error {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid key format %q (expected platform:videoId)", key)
	}
	platform, videoID := parts[0], parts[1]

	if store.Has(key) && !force {
		fmt.Printf("  skip  %s (already stored)\n", key)
		return nil
	}

	if dryRun {
		fmt.Printf("  would fetch %s\n", key)
		return nil
	}

	var (
		entry transcripts.Entry
		err   error
	)

	switch platform {
	case "youtube":
		entry, err = transcripts.FetchYouTube(videoID)
	case "tiktok":
		entry, err = transcripts.FetchTikTok(videoID)
	case "linkedin":
		fmt.Printf("  skip  %s (LinkedIn captions not supported)\n", key)
		return nil
	default:
		fmt.Printf("  skip  %s (unsupported platform)\n", key)
		return nil
	}

	if err != nil {
		return err
	}

	store.Set(key, entry)

	switch entry.Source {
	case "auto", "manual":
		fmt.Printf("  ✓     %s (%d chars)\n", key, len(entry.Text))
	default:
		fmt.Printf("  -     %s (no captions)\n", key)
	}

	return nil
}

// collectKeys returns "platform:videoId" keys for all YouTube and TikTok
// videos in the given report, deduplicated.
func collectKeys(report *internal.Report) []string {
	seen := map[string]bool{}
	var keys []string

	add := func(platform, videoID string) {
		if platform == "linkedin" {
			return
		}
		k := platform + ":" + videoID
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	for _, g := range report.VideoGroups {
		for platform, pd := range g.Platforms {
			add(platform, pd.VideoID)
		}
	}
	for _, u := range report.Unmatched {
		add(u.Platform, u.VideoID)
	}

	return keys
}
