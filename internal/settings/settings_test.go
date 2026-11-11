package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	s, err := Load(path)
	if err != nil || !s.NotifyChecksPassed || !s.NotifyReviews || s.NotifyRunSucceeded {
		t.Fatalf("defaults %+v, err %v", s, err)
	}

	s.ExtraRepos = []string{"o/r"}
	s.NotifyChecksPassed = false
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !slices.Equal(got.ExtraRepos, s.ExtraRepos) || got.NotifyChecksPassed || !got.NotifyReviews {
		t.Errorf("loaded %+v, err %v", got, err)
	}

	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err == nil || !got.NotifyChecksPassed {
		t.Errorf("broken file: %+v, err %v", got, err)
	}
}
