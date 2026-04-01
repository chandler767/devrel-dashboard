package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/devrel-dashboard/internal"
	"github.com/devrel-dashboard/internal/transcripts"
)

type videoRef struct {
	key string // "platform:videoId"
	url string // full video URL
}

func main() {
	all     := flag.Bool("all", false, "Fetch transcripts for all YouTube/TikTok videos in the latest report")
	video   := flag.String("video", "", "Fetch transcript for a single video (format: platform:videoId, e.g. youtube:abc123)")
	videoURL := flag.String("url", "", "Full URL of the video (required for TikTok when using --video)")
	refresh := flag.Bool("refresh", false, "Force re-fetch even if transcript already stored")
	dryRun  := flag.Bool("dry-run", false, "Print what would be fetched without writing any files")
	flag.Parse()

	if !*all && *video == "" {
		fmt.Fprintln(os.Stderr, "Usage: transcripts --all [--refresh] [--dry-run]")
		fmt.Fprintln(os.Stderr, "       transcripts --video youtube:abc123 [--refresh] [--dry-run]")
		fmt.Fprintln(os.Stderr, "       transcripts --video tiktok:123 --url https://www.tiktok.com/@user/video/123 [--refresh]")
		os.Exit(1)
	}

	store, err := transcripts.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading transcript store: %v\n", err)
		os.Exit(1)
	}

	if *video != "" {
		ref := videoRef{key: *video, url: *videoURL}
		if err := processOne(store, ref, *refresh, *dryRun); err != nil {
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

	// --all mode: collect video refs from the latest report
	report, err := internal.LoadPreviousReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading latest report: %v\n", err)
		os.Exit(1)
	}
	if report == nil {
		fmt.Fprintln(os.Stderr, "No report found. Run the fetch command first.")
		os.Exit(1)
	}

	refs := collectRefs(report)
	fmt.Printf("Found %d YouTube/TikTok video(s) in latest report.\n", len(refs))

	var toFetch []videoRef
	var skipped int
	for _, r := range refs {
		if store.Has(r.key) && !*refresh {
			skipped++
			continue
		}
		toFetch = append(toFetch, r)
	}

	if skipped > 0 {
		fmt.Printf("Skipping %d already-stored transcript(s) (use --refresh to re-fetch).\n", skipped)
	}
	if len(toFetch) == 0 {
		fmt.Println("Nothing to fetch.")
		return
	}

	var fetched, failed int
	for _, ref := range toFetch {
		if err := processOne(store, ref, true, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "  error %s: %v\n", ref.key, err)
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

// processOne fetches and stores a single transcript.
func processOne(store *transcripts.Store, ref videoRef, force bool, dryRun bool) error {
	parts := strings.SplitN(ref.key, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid key format %q (expected platform:videoId)", ref.key)
	}
	platform, videoID := parts[0], parts[1]

	if store.Has(ref.key) && !force {
		fmt.Printf("  skip  %s (already stored)\n", ref.key)
		return nil
	}

	if dryRun {
		fmt.Printf("  would fetch %s\n", ref.key)
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
		if ref.url == "" {
			fmt.Printf("  skip  %s (no URL available for TikTok)\n", ref.key)
			return nil
		}
		entry, err = transcripts.FetchTikTok(videoID, ref.url)
	case "linkedin":
		fmt.Printf("  skip  %s (LinkedIn captions not supported)\n", ref.key)
		return nil
	default:
		fmt.Printf("  skip  %s (unsupported platform)\n", ref.key)
		return nil
	}

	if err != nil {
		return err
	}

	store.Set(ref.key, entry)

	switch entry.Source {
	case "auto", "manual":
		fmt.Printf("  ✓     %s (%d chars)\n", ref.key, len(entry.Text))
	default:
		fmt.Printf("  -     %s (no captions)\n", ref.key)
	}

	return nil
}

// collectRefs returns videoRefs (key + URL) for all YouTube and TikTok
// videos in the given report, deduplicated.
func collectRefs(report *internal.Report) []videoRef {
	seen := map[string]bool{}
	var refs []videoRef

	add := func(platform, videoID, url string) {
		if platform == "linkedin" || platform == "tiktok" {
			return
		}
		k := platform + ":" + videoID
		if !seen[k] {
			seen[k] = true
			refs = append(refs, videoRef{key: k, url: url})
		}
	}

	for _, g := range report.VideoGroups {
		for platform, pd := range g.Platforms {
			add(platform, pd.VideoID, pd.URL)
		}
	}
	for _, u := range report.Unmatched {
		add(u.Platform, u.VideoID, u.URL)
	}

	return refs
}
