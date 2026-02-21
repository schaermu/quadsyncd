package quadlet

import (
	"os"
	"path/filepath"
	"strings"
)

// ValidExtensions are the recognized Podman Quadlet file extensions
var ValidExtensions = []string{
	".container",
	".volume",
	".network",
	".kube",
	".image",
	".build",
	".pod",
}

// IsQuadletFile returns true if the file has a valid quadlet extension
func IsQuadletFile(path string) bool {
	ext := filepath.Ext(path)
	for _, valid := range ValidExtensions {
		if ext == valid {
			return true
		}
	}
	return false
}

// DiscoverFiles finds all quadlet files in the specified directory
func DiscoverFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Check if this is a quadlet file
		if IsQuadletFile(path) {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

// DiscoverAllFiles finds all files in the specified directory, including
// non-quadlet companion files (e.g. environment files, config files).
// Hidden files and directories (names starting with ".") are skipped.
func DiscoverAllFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden files and directories (e.g. .git, .gitignore)
		if path != dir && strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

// UnitNameFromQuadlet converts a quadlet filename to its systemd unit name
// For example: myapp.container -> myapp.service
func UnitNameFromQuadlet(quadletPath string) string {
	base := filepath.Base(quadletPath)
	nameWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))
	return nameWithoutExt + ".service"
}

// IsRestartableQuadlet returns true if the quadlet file generates a service
// that should be restarted. Volume, network, image, and build files generate
// oneshot services that create resources and should not be restarted.
func IsRestartableQuadlet(path string) bool {
	ext := filepath.Ext(path)
	switch ext {
	case ".container", ".kube", ".pod":
		return true
	}
	return false
}

// RelativePath returns the relative path from baseDir to target
func RelativePath(baseDir, target string) (string, error) {
	return filepath.Rel(baseDir, target)
}
