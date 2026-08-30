package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// recentStatePath returns the path to the persisted recent-commands file,
// ~/.tgcp/recent.json (same ~/.tgcp directory used for debug.log).
func recentStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tgcp", "recent.json"), nil
}

type recentState struct {
	Names []string `json:"names"`
}

// loadRecentNames reads the persisted recent-commands list. Any error
// (missing file, unreadable home dir, corrupt JSON) is treated as "no
// history yet" rather than surfaced -- this is a best-effort UX nicety, not
// load-bearing state.
func loadRecentNames() []string {
	path, err := recentStatePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state recentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return state.Names
}

// saveRecentNames persists the recent-commands list. Best-effort: write
// failures (read-only home, disk full) are silently ignored since losing
// this history doesn't affect app correctness.
func saveRecentNames(names []string) {
	path, err := recentStatePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	data, err := json.Marshal(recentState{Names: names})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
