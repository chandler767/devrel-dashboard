package platforms

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/devrel-dashboard/internal"
)

const voyagerBase = "https://www.linkedin.com/voyager/api"

// LinkedInFetch fetches LinkedIn video posts via the Voyager (internal) API.
// Requires LINKEDIN_LI_AT and LINKEDIN_JSESSIONID from your browser cookies
// (DevTools → Storage → Cookies → linkedin.com).
// Optionally set LINKEDIN_PUBLIC_ID to your profile URL slug to skip auto-detection.
// Optionally set LINKEDIN_ORG_URNS (comma-separated) to also fetch from pages you manage.
func LinkedInFetch() ([]internal.Video, error) {
	liAt := os.Getenv("LINKEDIN_LI_AT")
	if liAt == "" {
		return nil, fmt.Errorf("linkedin: LINKEDIN_LI_AT is not set")
	}

	fmt.Println("  (using Voyager API with cookie auth)")
	vc := &voyagerClient{
		liAt:       liAt,
		jsessionid: os.Getenv("LINKEDIN_JSESSIONID"),
		bcookie:    os.Getenv("LINKEDIN_BCOOKIE"),
		bscookie:   os.Getenv("LINKEDIN_BSCOOKIE"),
	}

	// Person's public profile slug (the part after linkedin.com/in/)
	publicID := os.Getenv("LINKEDIN_PUBLIC_ID")
	var info meInfo
	if publicID == "" {
		var err error
		info, err = vc.me()
		if err != nil {
			return nil, fmt.Errorf("linkedin: could not detect profile ID: %w\n  Tip: set LINKEDIN_PUBLIC_ID=your-url-slug to skip auto-detection", err)
		}
		publicID = info.publicID
		fmt.Printf("  Auto-detected: slug=%s memberURN=%s fsdURN=%s\n", publicID, info.memberURN, info.fsdProfileURN)
	}

	var posts []voyagerPost

	personal, err := vc.personPosts(publicID, info)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  linkedin warning: personal posts failed: %v\n", err)
	} else {
		fmt.Printf("  %d personal video post(s)\n", len(personal))
		posts = append(posts, personal...)
	}

	if orgURNs := os.Getenv("LINKEDIN_ORG_URNS"); orgURNs != "" {
		for _, urn := range strings.Split(orgURNs, ",") {
			urn = strings.TrimSpace(urn)
			if urn == "" {
				continue
			}
			orgPosts, err := vc.orgPosts(urn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  linkedin warning: org %s: %v\n", urn, err)
				continue
			}
			fmt.Printf("  %d video post(s) from %s\n", len(orgPosts), urn)
			posts = append(posts, orgPosts...)
		}
	}

	videos := make([]internal.Video, 0, len(posts))
	for _, p := range posts {
		videos = append(videos, internal.Video{
			Platform:        "linkedin",
			ID:              p.urn,
			Title:           p.text,
			Views:           p.views,
			DurationSeconds: p.durationSecs,
			URL:             p.postURL,
			PublishedAt:     p.publishedAt,
		})
	}
	return videos, nil
}

// ── Voyager client ────────────────────────────────────────────────────────────

type voyagerClient struct {
	liAt, jsessionid, bcookie, bscookie string
}

type voyagerPost struct {
	urn, text, postURL, publishedAt string
	views                           int64
	durationSecs                    int
}

func (vc *voyagerClient) get(path string, params url.Values) ([]byte, error) {
	return vc.getWithReferer(path, params, "")
}

func (vc *voyagerClient) getWithReferer(path string, params url.Values, referer string) ([]byte, error) {
	u := voyagerBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	csrf := strings.Trim(vc.jsessionid, `"`)
	cookieVal := "li_at=" + vc.liAt + "; JSESSIONID=" + vc.jsessionid
	if vc.bcookie != "" {
		cookieVal += "; bcookie=" + vc.bcookie
	}
	if vc.bscookie != "" {
		cookieVal += "; bscookie=" + vc.bscookie
	}
	req.Header.Set("Cookie", cookieVal)
	req.Header.Set("Csrf-Token", csrf)
	req.Header.Set("X-RestLi-Protocol-Version", "2.0.0")
	req.Header.Set("X-Li-Lang", "en_US")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("x-li-page-instance", "urn:li:page:p_flagship3_profile_view_base;00000000-0000-0000-0000-000000000001")
	req.Header.Set("x-li-track", `{"clientVersion":"1.13.6535","mpVersion":"1.13.6535","osName":"web","timezoneOffset":-8,"timezone":"America/Los_Angeles","deviceFormFactor":"DESKTOP","mpName":"voyager-web","displayDensity":2,"displayWidth":1440,"displayHeight":900}`)
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Referer", "https://www.linkedin.com/")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d on %s: %.600s", resp.StatusCode, u, string(body))
	}
	return body, nil
}

type meInfo struct {
	publicID      string // URL slug e.g. "chandler-mayo"
	memberURN     string // e.g. "urn:li:member:214302444"
	fsdProfileURN string // e.g. "urn:li:fsd_profile:ACoAAAzF_uw..."
}

func (vc *voyagerClient) me() (meInfo, error) {
	body, err := vc.get("/me", nil)
	if err != nil {
		return meInfo{}, err
	}
	var r struct {
		PlainID     json.Number `json:"plainId"`
		MiniProfile *struct {
			PublicIdentifier string `json:"publicIdentifier"`
			EntityURN        string `json:"entityUrn"`
		} `json:"miniProfile"`
		Data *struct {
			PlainID     json.Number `json:"plainId"`
			MiniProfile *struct {
				PublicIdentifier string `json:"publicIdentifier"`
				EntityURN        string `json:"entityUrn"`
			} `json:"miniProfile"`
		} `json:"data"`
		Included []struct {
			PublicIdentifier string `json:"publicIdentifier"`
			EntityURN        string `json:"entityUrn"`
		} `json:"included"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return meInfo{}, fmt.Errorf("parse /me: %w (%.400s)", err, string(body))
	}

	info := meInfo{}

	// Extract publicIdentifier (URL slug) and entityUrn
	if r.MiniProfile != nil {
		info.publicID = r.MiniProfile.PublicIdentifier
		info.fsdProfileURN = fsMiniProfileToFsd(r.MiniProfile.EntityURN)
	}
	if r.Data != nil && r.Data.MiniProfile != nil {
		if info.publicID == "" {
			info.publicID = r.Data.MiniProfile.PublicIdentifier
		}
		if info.fsdProfileURN == "" {
			info.fsdProfileURN = fsMiniProfileToFsd(r.Data.MiniProfile.EntityURN)
		}
	}
	for _, inc := range r.Included {
		if info.publicID == "" && inc.PublicIdentifier != "" {
			info.publicID = inc.PublicIdentifier
		}
		if info.fsdProfileURN == "" && strings.Contains(inc.EntityURN, "fs_miniProfile") {
			info.fsdProfileURN = fsMiniProfileToFsd(inc.EntityURN)
		}
	}

	// Extract numeric member ID for URN
	plainID := r.PlainID
	if plainID.String() == "" && r.Data != nil {
		plainID = r.Data.PlainID
	}
	if id := plainID.String(); id != "" && id != "0" {
		info.memberURN = "urn:li:member:" + id
	}

	if info.publicID == "" && info.memberURN == "" && info.fsdProfileURN == "" {
		return meInfo{}, fmt.Errorf("could not find profile ID in /me response (%.400s)", string(body))
	}
	return info, nil
}

// fsMiniProfileToFsd converts urn:li:fs_miniProfile:X → urn:li:fsd_profile:X
func fsMiniProfileToFsd(urn string) string {
	if strings.HasPrefix(urn, "urn:li:fs_miniProfile:") {
		return "urn:li:fsd_profile:" + strings.TrimPrefix(urn, "urn:li:fs_miniProfile:")
	}
	return ""
}

// ── Feed element parsing ──────────────────────────────────────────────────────

// Legacy Voyager element format (profileUpdatesV2 / feed/updatesV2)
type liElement struct {
	CreatedAt    int64    `json:"createdAt"`
	EntityURN    string   `json:"entityUrn"`
	Value        liValue  `json:"value"`
	SocialDetail *struct {
		TotalSocialActivityCounts *struct {
			NumViews int64 `json:"numViews"`
		} `json:"totalSocialActivityCounts"`
	} `json:"socialDetail"`
}

type liValue struct {
	UpdateV2 *liUpdateV2 `json:"com.linkedin.voyager.feed.render.UpdateV2"`
}

type liUpdateV2 struct {
	Commentary *struct {
		Text struct{ Text string `json:"text"` } `json:"text"`
	} `json:"commentary"`
	Content *struct {
		VideoComponent *struct {
			VideoPlayMetadata *struct {
				Duration int64 `json:"duration"` // milliseconds
			} `json:"videoPlayMetadata"`
		} `json:"com.linkedin.voyager.feed.render.VideoComponent"`
	} `json:"content"`
	UpdateMetadata *struct {
		URN string `json:"urn"`
	} `json:"updateMetadata"`
}

// Dash element format (identity/dash/profileUpdates)
type dashElement struct {
	PublishedAt  int64  `json:"publishedAt"`
	EntityURN    string `json:"entityUrn"`
	Commentary   *struct {
		Text struct{ Text string `json:"text"` } `json:"text"`
	} `json:"commentary"`
	Content      *dashContent `json:"content"`
	SocialDetail *struct {
		TotalSocialActivityCounts *struct {
			NumViews int64 `json:"numViews"`
		} `json:"totalSocialActivityCounts"`
	} `json:"socialDetail"`
	UpdateMetadata *struct {
		URN string `json:"urn"`
	} `json:"updateMetadata"`
}

type dashContent struct {
	VideoComponent *struct {
		VideoPlayMetadata *struct {
			Duration int64 `json:"duration"` // milliseconds
		} `json:"videoPlayMetadata"`
	} `json:"com.linkedin.voyager.dash.feed.render.entity.update.content.video.VideoComponent"`
}

func (vc *voyagerClient) fetchPosts(path string, params url.Values) ([]voyagerPost, error) {
	return vc.fetchPostsRef(path, params, "")
}

func (vc *voyagerClient) fetchPostsRef(path string, params url.Values, referer string) ([]voyagerPost, error) {
	var all []voyagerPost
	start, count := 0, 50

	for {
		p := url.Values{}
		for k, v := range params {
			p[k] = v
		}
		p.Set("count", fmt.Sprintf("%d", count))
		p.Set("start", fmt.Sprintf("%d", start))

		body, err := vc.getWithReferer(path, p, referer)
		if err != nil {
			return nil, err
		}

		var feed struct {
			Elements []liElement `json:"elements"`
			Paging   struct {
				Total int `json:"total"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &feed); err != nil {
			return nil, fmt.Errorf("parse feed from %s: %w\n  body: %.400s", path, err, string(body))
		}

		if len(feed.Elements) == 0 && start == 0 {
			fmt.Fprintf(os.Stderr, "  linkedin: no elements found — raw response: %.600s\n", string(body))
		}

		for _, el := range feed.Elements {
			upd := el.Value.UpdateV2
			if upd == nil || upd.Content == nil || upd.Content.VideoComponent == nil {
				continue
			}

			urn := el.EntityURN
			if upd.UpdateMetadata != nil && upd.UpdateMetadata.URN != "" {
				urn = upd.UpdateMetadata.URN
			}
			if urn == "" {
				continue
			}

			text := "(untitled)"
			if upd.Commentary != nil {
				t := strings.TrimSpace(upd.Commentary.Text.Text)
				if idx := strings.IndexByte(t, '\n'); idx > 0 {
					t = t[:idx]
				}
				if len(t) > 120 {
					t = t[:120]
				}
				if t != "" {
					text = t
				}
			}

			var durationSecs int
			if vm := upd.Content.VideoComponent.VideoPlayMetadata; vm != nil {
				durationSecs = int(vm.Duration / 1000)
			}

			var views int64
			if el.SocialDetail != nil && el.SocialDetail.TotalSocialActivityCounts != nil {
				views = el.SocialDetail.TotalSocialActivityCounts.NumViews
			}

			publishedAt := ""
			if el.CreatedAt > 0 {
				publishedAt = time.Unix(el.CreatedAt/1000, 0).UTC().Format(time.RFC3339)
			}

			postURL := "https://www.linkedin.com/feed/update/" + url.PathEscape(urn) + "/"

			all = append(all, voyagerPost{
				urn:          urn,
				text:         text,
				views:        views,
				durationSecs: durationSecs,
				postURL:      postURL,
				publishedAt:  publishedAt,
			})
		}

		nextStart := start + len(feed.Elements)
		if nextStart >= feed.Paging.Total || len(feed.Elements) < count {
			break
		}
		start = nextStart
	}
	return all, nil
}

// fetchPostsDash handles the newer /identity/dash/profileUpdates endpoint
// which uses a different element structure from the legacy endpoints.
func (vc *voyagerClient) fetchPostsDash(path string, params url.Values) ([]voyagerPost, error) {
	return vc.fetchPostsDashRef(path, params, "")
}

func (vc *voyagerClient) fetchPostsDashRef(path string, params url.Values, referer string) ([]voyagerPost, error) {
	var all []voyagerPost
	start, count := 0, 50

	for {
		p := url.Values{}
		for k, v := range params {
			p[k] = v
		}
		p.Set("count", fmt.Sprintf("%d", count))
		p.Set("start", fmt.Sprintf("%d", start))

		body, err := vc.getWithReferer(path, p, referer)
		if err != nil {
			return nil, err
		}

		var feed struct {
			Elements []dashElement `json:"elements"`
			Paging   struct {
				Total int `json:"total"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &feed); err != nil {
			return nil, fmt.Errorf("parse dash feed from %s: %w\n  body: %.400s", path, err, string(body))
		}

		if len(feed.Elements) == 0 && start == 0 {
			fmt.Fprintf(os.Stderr, "  linkedin: no dash elements found — raw response: %.600s\n", string(body))
		}

		for _, el := range feed.Elements {
			if el.Content == nil || el.Content.VideoComponent == nil {
				continue
			}

			urn := el.EntityURN
			if el.UpdateMetadata != nil && el.UpdateMetadata.URN != "" {
				urn = el.UpdateMetadata.URN
			}
			if urn == "" {
				continue
			}

			text := "(untitled)"
			if el.Commentary != nil {
				t := strings.TrimSpace(el.Commentary.Text.Text)
				if idx := strings.IndexByte(t, '\n'); idx > 0 {
					t = t[:idx]
				}
				if len(t) > 120 {
					t = t[:120]
				}
				if t != "" {
					text = t
				}
			}

			var durationSecs int
			if vm := el.Content.VideoComponent.VideoPlayMetadata; vm != nil {
				durationSecs = int(vm.Duration / 1000)
			}

			var views int64
			if el.SocialDetail != nil && el.SocialDetail.TotalSocialActivityCounts != nil {
				views = el.SocialDetail.TotalSocialActivityCounts.NumViews
			}

			publishedAt := ""
			if el.PublishedAt > 0 {
				publishedAt = time.Unix(el.PublishedAt/1000, 0).UTC().Format(time.RFC3339)
			}

			postURL := "https://www.linkedin.com/feed/update/" + url.PathEscape(urn) + "/"

			all = append(all, voyagerPost{
				urn:          urn,
				text:         text,
				views:        views,
				durationSecs: durationSecs,
				postURL:      postURL,
				publishedAt:  publishedAt,
			})
		}

		nextStart := start + len(feed.Elements)
		if nextStart >= feed.Paging.Total || len(feed.Elements) < count {
			break
		}
		start = nextStart
	}
	return all, nil
}

func (vc *voyagerClient) personPosts(publicID string, info meInfo) ([]voyagerPost, error) {
	referer := "https://www.linkedin.com/in/" + publicID + "/recent-activity/videos/"

	type attempt struct {
		ep     string
		params url.Values
		dash   bool
	}
	var attempts []attempt

	// 1. Newer dash endpoint — uses profileUrn with fsd_profile format
	if info.fsdProfileURN != "" {
		attempts = append(attempts,
			attempt{"/identity/dash/profileUpdates", url.Values{
				"q":          {"memberShareFeed"},
				"profileUrn": {info.fsdProfileURN},
			}, true},
		)
	}

	// 2. Dash endpoint with member URN (some accounts use this)
	if info.memberURN != "" {
		attempts = append(attempts,
			attempt{"/identity/dash/profileUpdates", url.Values{
				"q":          {"memberShareFeed"},
				"profileUrn": {info.memberURN},
			}, true},
		)
	}

	// 3. Legacy endpoints with moduleKey=member-share (required by some LinkedIn regions)
	for _, id := range []string{publicID, info.memberURN} {
		if id == "" {
			continue
		}
		attempts = append(attempts,
			attempt{"/identity/profileUpdatesV2", url.Values{
				"q":         {"memberShareFeed"},
				"profileId": {id},
				"moduleKey": {"member-share"},
			}, false},
		)
	}

	// 4. Legacy endpoints without moduleKey
	for _, ep := range []string{"/identity/profileUpdatesV2", "/feed/updatesV2"} {
		for _, q := range []string{"memberShareFeed", "SELF", ""} {
			for _, id := range []string{publicID, info.memberURN, ""} {
				params := url.Values{}
				if q != "" {
					params.Set("q", q)
				}
				if id != "" {
					params.Set("profileId", id)
				}
				attempts = append(attempts, attempt{ep, params, false})
			}
		}
	}

	var lastErr error
	for _, a := range attempts {
		var posts []voyagerPost
		var err error
		if a.dash {
			posts, err = vc.fetchPostsDashRef(a.ep, a.params, referer)
		} else {
			posts, err = vc.fetchPostsRef(a.ep, a.params, referer)
		}
		if err == nil {
			fmt.Fprintf(os.Stderr, "  linkedin: success with %s params=%v\n", a.ep, a.params)
			return posts, nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "  linkedin: %s params=%v → %v\n", a.ep, a.params, err)
	}
	return nil, fmt.Errorf("all endpoint/param combinations failed — last error: %w", lastErr)
}

func (vc *voyagerClient) orgPosts(orgURN string) ([]voyagerPost, error) {
	// Try newer dash org endpoint first
	posts, err := vc.fetchPostsDash("/feed/dash/updatesV2", url.Values{
		"q":               {"organizationAndSponsoredUpdates"},
		"organizationUrn": {orgURN},
	})
	if err == nil {
		return posts, nil
	}
	fmt.Fprintf(os.Stderr, "  linkedin: dash org endpoint failed (%v), trying legacy\n", err)

	return vc.fetchPosts("/feed/updatesV2", url.Values{
		"q":               {"companyFeedByOrganization"},
		"organizationUrn": {orgURN},
	})
}
