package multirepo

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzNormalizeMergeKey(f *testing.F) {
	f.Add("simple/path.container")
	f.Add("../traversal")
	f.Add("/absolute/path")
	f.Add("")
	f.Add("C:\\windows\\path")
	f.Add("a/b/../c")
	f.Add(strings.Repeat("a/", 1000))
	f.Add(".")
	f.Add("..")
	f.Add("./relative")
	f.Add("a\x00b")

	f.Fuzz(func(t *testing.T, path string) {
		result, err := normalizeMergeKey(path)
		if err != nil {
			return
		}
		// Valid results must not be or start with ".." traversal.
		if result == ".." || strings.HasPrefix(result, "../") {
			t.Errorf("normalizeMergeKey(%q) = %q, contains traversal", path, result)
		}
		if filepath.IsAbs(result) {
			t.Errorf("normalizeMergeKey(%q) = %q, is absolute", path, result)
		}
	})
}
