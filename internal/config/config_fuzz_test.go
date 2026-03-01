package config

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzConfigLoad(f *testing.F) {
	// Seed with valid and invalid YAML.
	f.Add([]byte("repository:\n  url: https://example.com/repo\n  ref: refs/heads/main\npaths:\n  quadlet_dir: /tmp/q\n  state_dir: /tmp/s\n"))
	f.Add([]byte(""))
	f.Add([]byte("{invalid yaml"))
	f.Add([]byte("null"))
	f.Add([]byte("repositories:\n  - url: https://a.com/r\n    ref: refs/heads/main\n"))
	f.Add([]byte("serve:\n  enabled: true\n"))
	f.Add([]byte{0xFF, 0xFE}) // binary garbage

	f.Fuzz(func(t *testing.T, data []byte) {
		tmpFile := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			return
		}
		// Should never panic; errors are fine.
		_, _ = Load(tmpFile)
	})
}
