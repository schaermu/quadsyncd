package quadlet

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverAllFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a mix of quadlet, companion, and hidden files
	files := map[string]string{
		"myapp.container":         "[Container]\nImage=alpine\n",
		"myapp.env":               "FOO=bar\n",
		"shared.conf":             "key=value\n",
		"other.volume":            "[Volume]\n",
		".hidden":                 "should be ignored",
		".git/config":             "should be ignored",
		"subdir/nested.container": "[Container]\nImage=nginx\n",
		"subdir/nested.env":       "BAR=baz\n",
	}

	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := DiscoverAllFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Convert to relative paths for easier comparison
	var rel []string
	for _, p := range got {
		r, err := filepath.Rel(dir, p)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	sort.Strings(rel)

	want := []string{
		"myapp.container",
		"myapp.env",
		"other.volume",
		"shared.conf",
		"subdir/nested.container",
		"subdir/nested.env",
	}
	sort.Strings(want)

	if len(rel) != len(want) {
		t.Fatalf("DiscoverAllFiles() returned %d files, want %d:\ngot:  %v\nwant: %v", len(rel), len(want), rel, want)
	}
	for i := range want {
		if rel[i] != want[i] {
			t.Errorf("DiscoverAllFiles()[%d] = %q, want %q", i, rel[i], want[i])
		}
	}
}

func TestDiscoverAllFiles_ExcludesHiddenDirs(t *testing.T) {
	dir := t.TempDir()

	// Create a .git directory with some files
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git config"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a visible file
	if err := os.WriteFile(filepath.Join(dir, "app.container"), []byte("[Container]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverAllFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "app.container" {
		t.Errorf("expected app.container, got %s", filepath.Base(got[0]))
	}
}
