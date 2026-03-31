package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/devrel-dashboard/internal"
)

var liHashtagRe = regexp.MustCompile(`#(\w+)`)

const (
	liAPIBase    = "https://api.linkedin.com"
	liAPIVersion = "202603"
)

// ── API types ─────────────────────────────────────────────────────────────────

type liTokenResponse struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
}

type liPost struct {
	ID          string `json:"id"`
	Author      string `json:"author"`
	Commentary  string `json:"commentary"`
	PublishedAt int64  `json:"publishedAt"` // Unix ms
	Content     struct {
		Media *struct {
			ID        string `json:"id"`        // "urn:li:video:..." or "urn:li:image:..."
			Thumbnail string `json:"thumbnail"` // thumbnail URL (may be empty)
		} `json:"media"`
	} `json:"content"`
}

// liEngagement holds all engagement metrics for a single post.
type liEngagement struct {
	Views    int64
	Likes    int64
	Comments int64
	Shares   int64
	Clicks   int64
}

// ── Client ────────────────────────────────────────────────────────────────────

type liClient struct {
	accessToken string
	http        *http.Client
}

func (c *liClient) get(path string, params url.Values) ([]byte, error) {
	return c.getURL(liAPIBase + path + func() string {
		if len(params) > 0 {
			return "?" + params.Encode()
		}
		return ""
	}())
}

// getRestLi builds a URL with RestLi-style list params that must not be
// percent-encoded (e.g. ugcPosts=List(urn:li:ugcPost:123)).
// encoded holds normal key=value pairs; raw is appended verbatim after &.
func (c *liClient) getRestLi(path string, encoded url.Values, raw string) ([]byte, error) {
	u := liAPIBase + path
	q := encoded.Encode()
	if raw != "" {
		if q != "" {
			q += "&"
		}
		q += raw
	}
	if q != "" {
		u += "?" + q
	}
	return c.getURL(u)
}

func (c *liClient) getURL(u string) ([]byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("LinkedIn-Version", liAPIVersion)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
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

// ── Token refresh ─────────────────────────────────────────────────────────────

// refreshToken uses the stored refresh token to get a new access token and
// writes it back to .env. If any credential is missing, returns the current
// LINKEDIN_ACCESS_TOKEN as-is (allows using a manually set token).
func refreshToken() (string, error) {
	clientID := os.Getenv("LINKEDIN_CLIENT_ID")
	clientSecret := os.Getenv("LINKEDIN_CLIENT_SECRET")
	refreshTok := os.Getenv("LINKEDIN_REFRESH_TOKEN")

	if clientID == "" || clientSecret == "" || refreshTok == "" {
		// No refresh credentials — use whatever access token is set
		tok := os.Getenv("LINKEDIN_ACCESS_TOKEN")
		if tok == "" {
			return "", fmt.Errorf("linkedin: no access token and no refresh credentials (set LINKEDIN_ACCESS_TOKEN or LINKEDIN_CLIENT_ID + LINKEDIN_CLIENT_SECRET + LINKEDIN_REFRESH_TOKEN)")
		}
		return tok, nil
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}

	resp, err := http.PostForm("https://www.linkedin.com/oauth/v2/accessToken", form)
	if err != nil {
		return "", fmt.Errorf("linkedin token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("linkedin token refresh HTTP %d: %.400s", resp.StatusCode, string(body))
	}

	var tok liTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("linkedin token refresh parse: %w", err)
	}

	if err := updateEnvValue("LINKEDIN_ACCESS_TOKEN", tok.AccessToken); err != nil {
		fmt.Fprintf(os.Stderr, "  linkedin warning: could not save new access token: %v\n", err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refreshTok {
		if err := updateEnvValue("LINKEDIN_REFRESH_TOKEN", tok.RefreshToken); err != nil {
			fmt.Fprintf(os.Stderr, "  linkedin warning: could not save rotated refresh token: %v\n", err)
		}
	}

	return tok.AccessToken, nil
}

// ── Posts fetching ────────────────────────────────────────────────────────────

// fetchPosts fetches all video posts for the given author URN (person or org),
// paginating until all posts are retrieved.
func (c *liClient) fetchPosts(authorURN string) ([]liPost, error) {
	var all []liPost
	count := 100
	start := 0

	for {
		params := url.Values{
			"author": {authorURN},
			"q":      {"author"},
			"count":  {fmt.Sprintf("%d", count)},
			"start":  {fmt.Sprintf("%d", start)},
		}

		body, err := c.get("/rest/posts", params)
		if err != nil {
			return nil, err
		}

		var result struct {
			Elements []liPost `json:"elements"`
			Paging   struct {
				Total int `json:"total"`
				Count int `json:"count"`
				Start int `json:"start"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse /rest/posts: %w (%.400s)", err, string(body))
		}

		for _, p := range result.Elements {
			// Only keep posts authored directly by the requested entity (skip reshares)
			if p.Author != authorURN {
				continue
			}
			// Only keep video posts
			if p.Content.Media == nil {
				continue
			}
			if !strings.HasPrefix(p.Content.Media.ID, "urn:li:video:") {
				continue
			}
			all = append(all, p)
		}

		nextStart := start + len(result.Elements)
		if nextStart >= result.Paging.Total || len(result.Elements) < count {
			break
		}
		start = nextStart
	}

	return all, nil
}

// batchOrgPostImpressions fetches engagement metrics for all posts of an org in
// a single API call. Returns a map of postURN → liEngagement.
// The RestLi List(...) syntax must not be percent-encoded, so we use getRestLi.
func (c *liClient) batchOrgPostImpressions(orgURN string, postURNs []string) (map[string]liEngagement, error) {
	encoded := url.Values{
		"q":                    {"organizationalEntity"},
		"organizationalEntity": {orgURN},
	}

	var ugcPosts, shares []string
	for _, urn := range postURNs {
		if strings.HasPrefix(urn, "urn:li:ugcPost:") {
			ugcPosts = append(ugcPosts, url.QueryEscape(urn))
		} else {
			shares = append(shares, url.QueryEscape(urn))
		}
	}

	var rawParts []string
	if len(ugcPosts) > 0 {
		rawParts = append(rawParts, "ugcPosts=List("+strings.Join(ugcPosts, ",")+")")
	}
	if len(shares) > 0 {
		rawParts = append(rawParts, "shares=List("+strings.Join(shares, ",")+")")
	}
	raw := strings.Join(rawParts, "&")

	body, err := c.getRestLi("/rest/organizationalEntityShareStatistics", encoded, raw)
	if err != nil {
		return nil, err
	}

	var result struct {
		Elements []struct {
			UgcPost              string `json:"ugcPost"`
			Share                string `json:"share"`
			TotalShareStatistics struct {
				ImpressionCount int64 `json:"impressionCount"`
				LikeCount       int64 `json:"likeCount"`
				CommentCount    int64 `json:"commentCount"`
				ShareCount      int64 `json:"shareCount"`
				ClickCount      int64 `json:"clickCount"`
			} `json:"totalShareStatistics"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse /rest/organizationalEntityShareStatistics: %w (%.400s)", err, string(body))
	}

	out := make(map[string]liEngagement, len(result.Elements))
	for _, el := range result.Elements {
		key := el.UgcPost
		if key == "" {
			key = el.Share
		}
		if key != "" {
			s := el.TotalShareStatistics
			out[key] = liEngagement{
				Views:    s.ImpressionCount,
				Likes:    s.LikeCount,
				Comments: s.CommentCount,
				Shares:   s.ShareCount,
				Clicks:   s.ClickCount,
			}
		}
	}
	return out, nil
}

// batchPersonalPostViews fetches engagement metrics for personal posts in one call.
// Returns a map of postURN → liEngagement.
func (c *liClient) batchPersonalPostViews(postURNs []string) (map[string]liEngagement, error) {
	escaped := make([]string, len(postURNs))
	for i, u := range postURNs {
		escaped[i] = url.QueryEscape(u)
	}
	raw := "entities=List(" + strings.Join(escaped, ",") + ")"
	body, err := c.getRestLi("/rest/socialMetricsV2", nil, raw)
	if err != nil {
		return nil, err
	}

	var result struct {
		Results map[string]struct {
			TotalShareStatistics struct {
				ImpressionCount int64 `json:"impressionCount"`
				LikeCount       int64 `json:"likeCount"`
				CommentCount    int64 `json:"commentCount"`
				ShareCount      int64 `json:"shareCount"`
				ClickCount      int64 `json:"clickCount"`
			} `json:"totalShareStatistics"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse /rest/socialMetricsV2: %w", err)
	}

	out := make(map[string]liEngagement, len(result.Results))
	for urn, entry := range result.Results {
		s := entry.TotalShareStatistics
		out[urn] = liEngagement{
			Views:    s.ImpressionCount,
			Likes:    s.LikeCount,
			Comments: s.CommentCount,
			Shares:   s.ShareCount,
			Clicks:   s.ClickCount,
		}
	}
	return out, nil
}


// ── LinkedInFetch ─────────────────────────────────────────────────────────────

// LinkedInFetch fetches LinkedIn video posts via the Community Management API.
// Requires LINKEDIN_ACCESS_TOKEN (auto-refreshed if LINKEDIN_CLIENT_ID,
// LINKEDIN_CLIENT_SECRET, and LINKEDIN_REFRESH_TOKEN are set).
// Set LINKEDIN_PERSON_URN to fetch personal video posts.
// Set LINKEDIN_ORG_URNS (comma-separated) to fetch from LinkedIn Pages.
func LinkedInFetch() ([]internal.Video, error) {
	accessToken, err := refreshToken()
	if err != nil {
		return nil, err
	}

	client := &liClient{
		accessToken: accessToken,
		http:        &http.Client{Timeout: 30 * time.Second},
	}

	type sourcePost struct {
		post   liPost
		orgURN string // empty for personal posts
	}
	var sourcePosts []sourcePost

	// Fetch personal posts
	if personURN := os.Getenv("LINKEDIN_PERSON_URN"); personURN != "" {
		posts, err := client.fetchPosts(personURN)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  linkedin warning: personal posts (%s): %v\n", personURN, err)
		} else {
			for _, p := range posts {
				sourcePosts = append(sourcePosts, sourcePost{post: p})
			}
		}
	}

	// orgEngagement maps orgURN → (postURN → liEngagement), populated in batch
	orgEngagement := map[string]map[string]liEngagement{}

	// Fetch org posts, then batch-fetch their engagement metrics
	if orgURNsStr := os.Getenv("LINKEDIN_ORG_URNS"); orgURNsStr != "" {
		for _, orgURN := range strings.Split(orgURNsStr, ",") {
			orgURN = strings.TrimSpace(orgURN)
			if orgURN == "" {
				continue
			}
			posts, err := client.fetchPosts(orgURN)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  linkedin warning: org %s: %v\n", orgURN, err)
				continue
			}
			for _, p := range posts {
				sourcePosts = append(sourcePosts, sourcePost{post: p, orgURN: orgURN})
			}

			// Batch-fetch engagement metrics for all posts in this org (1 API call)
			postURNs := make([]string, len(posts))
			for i, p := range posts {
				postURNs[i] = p.ID
			}
			if len(postURNs) > 0 {
				eng, err := client.batchOrgPostImpressions(orgURN, postURNs)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  linkedin warning: batchOrgPostImpressions(%s): %v\n", orgURN, err)
				} else {
					orgEngagement[orgURN] = eng
				}
			}
		}
	}

	// Batch-fetch personal post engagement metrics (1 API call total)
	personalEngagement := map[string]liEngagement{}
	var personalURNs []string
	for _, sp := range sourcePosts {
		if sp.orgURN == "" {
			personalURNs = append(personalURNs, sp.post.ID)
		}
	}
	if len(personalURNs) > 0 {
		if m, err := client.batchPersonalPostViews(personalURNs); err != nil {
			fmt.Fprintf(os.Stderr, "  linkedin warning: batchPersonalPostViews: %v\n", err)
		} else {
			personalEngagement = m
		}
	}

	videos := make([]internal.Video, 0, len(sourcePosts))
	for _, sp := range sourcePosts {
		p := sp.post

		// Resolve engagement from batch results
		var eng liEngagement
		if sp.orgURN != "" {
			if m, ok := orgEngagement[sp.orgURN]; ok {
				eng = m[p.ID]
			}
		} else {
			eng = personalEngagement[p.ID]
		}

		// Title: first line of commentary, capped at 120 chars
		title := "(untitled)"
		commentary := strings.TrimSpace(p.Commentary)
		if commentary != "" {
			firstLine := commentary
			if idx := strings.IndexByte(firstLine, '\n'); idx > 0 {
				firstLine = firstLine[:idx]
			}
			if len(firstLine) > 120 {
				firstLine = firstLine[:120]
			}
			title = firstLine
		}

		// Extract hashtags from full commentary
		var tags []string
		for _, match := range liHashtagRe.FindAllStringSubmatch(commentary, -1) {
			tags = append(tags, match[1])
		}

		// Thumbnail from media content
		thumbnail := ""
		if p.Content.Media != nil {
			thumbnail = p.Content.Media.Thumbnail
		}

		publishedAt := ""
		if p.PublishedAt > 0 {
			publishedAt = time.UnixMilli(p.PublishedAt).UTC().Format(time.RFC3339)
		}

		postURL := "https://www.linkedin.com/feed/update/" + url.PathEscape(p.ID) + "/"

		// LinkedIn's socialActions/comments endpoint requires partner-tier API access
		// (partnerApiSocialActions permission) which is not available to standard apps.
		var commentTexts []string

		videos = append(videos, internal.Video{
			Platform:        "linkedin",
			ID:              p.ID,
			Title:           title,
			Author:          p.Author,
			Views:           eng.Views,
			Likes:           eng.Likes,
			Comments:        eng.Comments,
			Shares:          eng.Shares,
			Clicks:          eng.Clicks,
			CommentTexts:    commentTexts,
			Thumbnail:       thumbnail,
			Description:     commentary,
			Tags:            tags,
			DurationSeconds: 0, // not available from posts API
			URL:             postURL,
			PublishedAt:     publishedAt,
		})
	}

	return videos, nil
}

// ── LinkedInAuth ──────────────────────────────────────────────────────────────

// LinkedInAuth runs a one-time OAuth 2.0 authorization code flow.
// It prints the auth URL, starts a local server to capture the redirect,
// exchanges the code for tokens, and saves them to .env.
func LinkedInAuth() error {
	clientID := os.Getenv("LINKEDIN_CLIENT_ID")
	clientSecret := os.Getenv("LINKEDIN_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("linkedin: LINKEDIN_CLIENT_ID and LINKEDIN_CLIENT_SECRET must be set in .env")
	}

	redirectURI := "http://localhost:8080/callback"
	scopes := "r_organization_social rw_organization_admin"

	authURL := "https://www.linkedin.com/oauth/v2/authorization?" + url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"scope":         {scopes},
	}.Encode()

	fmt.Println("\nOpen this URL in your browser to authorize:")
	fmt.Println()
	fmt.Println(" ", authURL)
	fmt.Println()
	fmt.Println("Waiting for redirect on http://localhost:8080/callback ...")

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: ":8080", Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			fmt.Fprintf(w, "Authorization failed: %s — %s\n", errParam, desc)
			errCh <- fmt.Errorf("authorization denied: %s — %s", errParam, desc)
			return
		}
		fmt.Fprintln(w, "Authorization successful! You can close this tab.")
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("local server: %w", err)
		}
	}()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timed out waiting for OAuth callback")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	// Exchange code for tokens
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {clientID},
		"client_secret": {clientSecret},
	}
	resp, err := http.PostForm("https://www.linkedin.com/oauth/v2/accessToken", form)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange HTTP %d: %.400s", resp.StatusCode, string(body))
	}

	var tok liTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("token exchange parse: %w", err)
	}

	if err := updateEnvValue("LINKEDIN_ACCESS_TOKEN", tok.AccessToken); err != nil {
		return fmt.Errorf("save access token: %w", err)
	}
	if tok.RefreshToken != "" {
		if err := updateEnvValue("LINKEDIN_REFRESH_TOKEN", tok.RefreshToken); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("Tokens saved to .env!")
	if tok.ExpiresIn > 0 {
		fmt.Printf("Access token expires in %d seconds (~%.0f days)\n", tok.ExpiresIn, float64(tok.ExpiresIn)/86400)
	}
	if tok.RefreshTokenExpiresIn > 0 {
		fmt.Printf("Refresh token expires in %d seconds (~%.0f days)\n", tok.RefreshTokenExpiresIn, float64(tok.RefreshTokenExpiresIn)/86400)
	}
	return nil
}
