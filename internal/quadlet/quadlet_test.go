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

func TestIsQuadletFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Valid quadlet extensions
		{"myapp.container", true},
		{"db.volume", true},
		{"net.network", true},
		{"app.kube", true},
		{"base.image", true},
		{"ci.build", true},
		{"group.pod", true},
		// Full paths with valid extensions
		{"/path/to/myapp.container", true},
		// Invalid extensions
		{"readme.txt", false},
		{"config.yaml", false},
		{"myapp.env", false},
		// No extension
		{"Makefile", false},
		// Empty string
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := IsQuadletFile(tc.path)
			if got != tc.want {
				t.Errorf("IsQuadletFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestDiscoverFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a mix of quadlet and non-quadlet files
	filesToCreate := []string{
		"myapp.container",
		"db.volume",
		"frontend.network",
		"readme.txt",
		"config.yaml",
		"myapp.env",
	}
	for _, f := range filesToCreate {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := DiscoverFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Convert to basenames for comparison
	var basenames []string
	for _, p := range got {
		basenames = append(basenames, filepath.Base(p))
	}
	sort.Strings(basenames)

	want := []string{"db.volume", "frontend.network", "myapp.container"}
	if len(basenames) != len(want) {
		t.Fatalf("DiscoverFiles() returned %d files, want %d:\ngot:  %v\nwant: %v", len(basenames), len(want), basenames, want)
	}
	for i := range want {
		if basenames[i] != want[i] {
			t.Errorf("DiscoverFiles()[%d] = %q, want %q", i, basenames[i], want[i])
		}
	}
}

func TestDiscoverFiles_NonExistentDir(t *testing.T) {
	_, err := DiscoverFiles("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent directory, got nil")
	}
}

func TestUnitNameFromQuadlet(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp.container", "myapp.service"},
		{"db.volume", "db-volume.service"},
		{"net.network", "net-network.service"},
		{"app.kube", "app.service"},
		{"base.image", "base-image.service"},
		{"ci.build", "ci-build.service"},
		{"group.pod", "group.service"},
		{"/path/to/db.volume", "db-volume.service"},
		{"/deep/nested/path/myapp.container", "myapp.service"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := UnitNameFromQuadlet(tc.input)
			if got != tc.want {
				t.Errorf("UnitNameFromQuadlet(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRelativePath(t *testing.T) {
	tests := []struct {
		name    string
		baseDir string
		target  string
		want    string
	}{
		{
			name:    "target inside baseDir",
			baseDir: "/home/user/quadlets",
			target:  "/home/user/quadlets/myapp.container",
			want:    "myapp.container",
		},
		{
			name:    "nested target inside baseDir",
			baseDir: "/home/user/quadlets",
			target:  "/home/user/quadlets/sub/dir/app.volume",
			want:    "sub/dir/app.volume",
		},
		{
			name:    "target outside baseDir",
			baseDir: "/home/user/quadlets",
			target:  "/home/user/other/file.txt",
			want:    "../other/file.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RelativePath(tc.baseDir, tc.target)
			if err != nil {
				t.Fatalf("RelativePath(%q, %q) returned unexpected error: %v", tc.baseDir, tc.target, err)
			}
			if got != tc.want {
				t.Errorf("RelativePath(%q, %q) = %q, want %q", tc.baseDir, tc.target, got, tc.want)
			}
		})
	}
}
