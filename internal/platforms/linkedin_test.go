package platforms

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// loadTestCreds loads .env and returns (accessToken, orgURN).
// Skips the test if credentials are not available.
func loadTestCreds(t *testing.T) (accessToken, orgURN string) {
	t.Helper()
	_ = godotenv.Load("../../.env")

	accessToken = os.Getenv("LINKEDIN_ACCESS_TOKEN")
	orgURN = os.Getenv("LINKEDIN_ORG_URNS")
	if accessToken == "" || orgURN == "" {
		t.Skip("LINKEDIN_ACCESS_TOKEN and LINKEDIN_ORG_URNS required")
	}
	// Use only the first org if multiple are configured
	for i, c := range orgURN {
		if c == ',' {
			orgURN = orgURN[:i]
			break
		}
	}
	return
}

func newTestClient(accessToken string) *liClient {
	return &liClient{
		accessToken: accessToken,
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

// TestLinkedIn_FetchFirstTwoPosts fetches only the first 2 posts for the org
// and verifies the response parses correctly. 1 API call.
func TestLinkedIn_FetchFirstTwoPosts(t *testing.T) {
	accessToken, orgURN := loadTestCreds(t)
	client := newTestClient(accessToken)

	params := url.Values{
		"author": {orgURN},
		"q":      {"author"},
		"count":  {"2"},
		"start":  {"0"},
	}
	body, err := client.get("/rest/posts", params)
	if err != nil {
		t.Fatalf("fetch posts: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	t.Logf("posts response (%d bytes): %.500s", len(body), string(body))
}

// TestLinkedIn_BatchOrgImpressions fetches 2 posts then calls
// batchOrgPostImpressions with their IDs. 2 API calls total.
func TestLinkedIn_BatchOrgImpressions(t *testing.T) {
	accessToken, orgURN := loadTestCreds(t)
	client := newTestClient(accessToken)

	// Fetch 2 posts
	posts, err := fetchNPosts(client, orgURN, 2)
	if err != nil {
		t.Fatalf("fetch posts: %v", err)
	}
	if len(posts) == 0 {
		t.Skip("no posts returned for org")
	}

	postURNs := make([]string, len(posts))
	for i, p := range posts {
		postURNs[i] = p.ID
	}

	impressions, err := client.batchOrgPostImpressions(orgURN, postURNs)
	if err != nil {
		t.Fatalf("batchOrgPostImpressions: %v", err)
	}
	t.Logf("impressions for %d posts: %v", len(postURNs), impressions)
}

// fetchNPosts fetches up to n posts for the given author URN (no video filter).
func fetchNPosts(c *liClient, authorURN string, n int) ([]liPost, error) {
	params := url.Values{
		"author": {authorURN},
		"q":      {"author"},
		"count":  {"2"},
		"start":  {"0"},
	}
	body, err := c.get("/rest/posts", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Elements []liPost `json:"elements"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result.Elements) > n {
		return result.Elements[:n], nil
	}
	return result.Elements, nil
}
