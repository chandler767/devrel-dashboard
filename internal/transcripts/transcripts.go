package transcripts

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const storeFile = "transcripts.json"
const storeJSFile = "transcripts.js"

// Entry holds the transcript for a single video.
type Entry struct {
	FetchedAt string `json:"fetched_at"`
	Lang      string `json:"lang"`
	Text      string `json:"text"`
	Source    string `json:"source"` // "auto" | "manual" | "none"
}

// Store is the full transcripts.json structure.
type Store struct {
	Version     int              `json:"version"`
	UpdatedAt   string           `json:"updated_at"`
	Transcripts map[string]Entry `json:"transcripts"` // keyed "platform:videoId"
}

// Load reads transcripts.json from the current working directory.
// Returns an empty store (no error) if the file does not exist yet.
func Load() (*Store, error) {
	data, err := os.ReadFile(storeFile)
	if os.IsNotExist(err) {
		return &Store{
			Version:     1,
			Transcripts: map[string]Entry{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", storeFile, err)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", storeFile, err)
	}
	if s.Transcripts == nil {
		s.Transcripts = map[string]Entry{}
	}
	return &s, nil
}

// Save writes the store atomically to transcripts.json and also writes
// transcripts.js for file:// protocol support (matching existing report wrappers).
func (s *Store) Save() error {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.Version = 1

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcripts: %w", err)
	}

	// Atomic write: write to .tmp then rename
	tmp := storeFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, storeFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}

	// JS wrapper for file:// support
	jsContent := fmt.Sprintf("window.__devrelTranscripts=%s;", string(data))
	return os.WriteFile(storeJSFile, []byte(jsContent), 0644)
}

// Has returns true if the given key ("platform:videoId") is present in the
// store, even if the transcript text is empty (Source: "none").
func (s *Store) Has(key string) bool {
	_, ok := s.Transcripts[key]
	return ok
}

// Set stores an entry for the given key.
func (s *Store) Set(key string, entry Entry) {
	s.Transcripts[key] = entry
}
