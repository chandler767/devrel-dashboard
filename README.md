# DevRel Dashboard

Track short-form video views across YouTube, TikTok, and LinkedIn in one place.

Run the fetch script once a day to pull stats, auto-group the same video posted to multiple platforms, and push a report to GitHub Pages.

---

## How it works

1. `go run ./cmd/fetch` fetches videos from all 3 APIs, groups matches by title similarity + duration, writes a timestamped JSON report to `reports/`, and pushes to GitHub.
2. `index.html` (served by GitHub Pages) loads the most recent report and lets you browse historical ones via a date dropdown (or `?report=<id>` query string).

---

## Setup

### 1. Install Go

Requires Go 1.21+. Download at https://go.dev/dl/

### 2. Clone and install dependencies

```bash
git clone https://github.com/YOUR_USERNAME/devrel-dashboard.git
cd devrel-dashboard
go mod tidy
```

### 3. Copy and fill in credentials

```bash
cp .env.example .env
# Edit .env with your API credentials (see sections below)
```

### 4. Enable GitHub Pages

In your repo settings → Pages → Source: **Deploy from branch** → Branch: `main` / `/ (root)`.

Your dashboard will be live at `https://YOUR_USERNAME.github.io/devrel-dashboard/`.

---

## Credentials Setup

### YouTube

No login required. You just need a free API key:

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → create a project.
2. Enable **YouTube Data API v3**.
3. Go to **Credentials → Create Credentials → API key**. Copy it into `.env` as `YOUTUBE_API_KEY`.
4. Find your channel ID: YouTube Studio → Settings → Channel → Advanced settings (starts with `UC...`). Set `YOUTUBE_CHANNEL_ID`.

That's it — no OAuth, no refresh tokens.

### TikTok

No API key or login required. Install `yt-dlp` once:

```bash
brew install yt-dlp
```

Then set `TIKTOK_USERNAME` in `.env` to your TikTok handle (without the `@`). The script calls `yt-dlp` automatically each run.

### LinkedIn

The **Community Management API** is required for video analytics and **must be the only product on its app** (LinkedIn restriction). You need a dedicated app:

1. Create a **new** app at [LinkedIn Developer Portal](https://www.linkedin.com/developers/apps). Name it anything (e.g. "DevRel Dashboard Analytics"). Associate it with your LinkedIn company page.
2. Under the **Products** tab, click **Request access** next to **Community Management API**. Do not add any other products — the request will be rejected if other products are present. Approval is usually fast (minutes to hours).
3. Under **Auth** → Authorized Redirect URLs, add `http://localhost:8080/callback`.
4. Copy the **Client ID** and **Client Secret** from the **Auth** tab into `.env`.
5. Get your OAuth tokens:

```bash
# Step 1: Open this in a browser (replace YOUR_CLIENT_ID):
open "https://www.linkedin.com/oauth/v2/authorization?response_type=code&client_id=YOUR_CLIENT_ID&redirect_uri=http://localhost:8080/callback&scope=r_organization_social&state=random123"

# You'll be redirected to http://localhost:8080/callback?code=XXXXXX
# Copy the "code" value from the URL, then run:
curl -X POST https://www.linkedin.com/oauth/v2/accessToken \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code&code=PASTE_CODE_HERE&redirect_uri=http://localhost:8080/callback&client_id=YOUR_CLIENT_ID&client_secret=YOUR_CLIENT_SECRET"

# Copy "access_token" and "refresh_token" from the response into .env
# access_token expires in 60 days; refresh_token lasts 365 days
# The script auto-refreshes access_token on each run

# Step 2: Find your person URN — no API call needed.
# Go to linkedin.com → your profile → click "More" → "Save to PDF"
# The PDF filename contains your numeric ID, e.g. "Profile_1234567890.pdf"
# OR: view page source on your profile and search for "fsd_profile:"
# Set LINKEDIN_PERSON_URN=urn:li:person:THAT_NUMERIC_ID
```

---

## Running the fetch script

```bash
# Full run — fetches all platforms, saves report, commits and pushes
go run ./cmd/fetch

# Dry run — prints JSON report to stdout, no files written, no git commit
go run ./cmd/fetch --dry-run

# Skip a platform (useful when one API is down)
go run ./cmd/fetch --skip-tiktok
go run ./cmd/fetch --skip-linkedin

# Build a binary for repeated use
go build -o devrel-fetch ./cmd/fetch
./devrel-fetch
```

---

## Video grouping

Videos are automatically grouped across platforms when they have:
- **Title similarity ≥ 70%** (Jaro-Winkler algorithm, after normalizing titles)
- **Duration within ±5 seconds**

Videos that don't match any cross-platform pair appear in the "Platform-Only Videos" section.

---

## Dashboard URL

- Default (most recent report): `https://YOUR_USERNAME.github.io/devrel-dashboard/`
- Specific report: `https://YOUR_USERNAME.github.io/devrel-dashboard/?report=2024-01-15T10-30-00Z`

---

## Local preview

```bash
python3 -m http.server 8080
# Open http://localhost:8080
```
