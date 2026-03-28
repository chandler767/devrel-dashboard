package internal

import (
	"encoding/json"
	"os"
	"sort"
)

const rejectedFile = "rejected_videos.json"

type rejectedStore struct {
	Rejected []string `json:"rejected"`
}

// LoadRejectedVideoIDs reads rejected_videos.json and returns a set of
// "platform:id" keys for videos the user has chosen not to include.
func LoadRejectedVideoIDs() map[string]bool {
	data, err := os.ReadFile(rejectedFile)
	if err != nil {
		return map[string]bool{}
	}
	var store rejectedStore
	if err := json.Unmarshal(data, &store); err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(store.Rejected))
	for _, id := range store.Rejected {
		out[id] = true
	}
	return out
}

// SaveRejectedVideoID adds platform:id to rejected_videos.json.
// No-ops if the entry already exists.
func SaveRejectedVideoID(platform, id string) error {
	rejected := LoadRejectedVideoIDs()
	key := platform + ":" + id
	if rejected[key] {
		return nil
	}
	rejected[key] = true

	keys := make([]string, 0, len(rejected))
	for k := range rejected {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	store := rejectedStore{Rejected: keys}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(rejectedFile, data, 0644)
}
