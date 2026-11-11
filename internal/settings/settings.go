// Package settings keeps the user's choices in a JSON file.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Settings struct {
	// ExtraRepos are owner/name repositories to watch for runs beside
	// those of the user's pull requests.
	ExtraRepos         []string `json:"extraRepos"`
	NotifyChecksPassed bool     `json:"notifyChecksPassed"`
	NotifyRunSucceeded bool     `json:"notifyRunSucceeded"`
	NotifyReviews      bool     `json:"notifyReviews"`
	// DismissedRuns are the failed runs the user has seen to, by run ID,
	// and when each was dismissed.
	DismissedRuns map[int64]time.Time `json:"dismissedRuns,omitempty"`
	// Appearance is "light" or "dark"; empty follows the system.
	Appearance string `json:"appearance,omitempty"`
	// AccentColor, BackgroundColor and TitleBarColor replace the theme's
	// colors, as "#rrggbb"; empty keeps the theme's.
	AccentColor     string `json:"accentColor,omitempty"`
	BackgroundColor string `json:"backgroundColor,omitempty"`
	TitleBarColor   string `json:"titleBarColor,omitempty"`
}

// Failures always notify; these are the defaults for the rest.
func Default() Settings {
	return Settings{NotifyChecksPassed: true, NotifyReviews: true}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "whippy", "settings.json"), nil
}

// Load returns the defaults where there is no file yet.
func Load(path string) (Settings, error) {
	s := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), err
	}
	return s, nil
}

func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
