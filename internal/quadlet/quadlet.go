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

// unitServiceSuffix maps quadlet extensions to the infix that Podman's Quadlet
// generator inserts between the base name and ".service".  Extensions absent
// from this map (e.g. .container, .kube, .pod) produce plain "<name>.service".
var unitServiceSuffix = map[string]string{
	".volume":  "-volume",
	".network": "-network",
	".image":   "-image",
	".build":   "-build",
}

// UnitNameFromQuadlet converts a quadlet filename to its systemd unit name.
// Podman's Quadlet generator appends a type-specific infix for some types:
//
//	.container → <name>.service
//	.volume    → <name>-volume.service
//	.network   → <name>-network.service
//	.kube      → <name>.service
//	.image     → <name>-image.service
//	.build     → <name>-build.service
//	.pod       → <name>.service
func UnitNameFromQuadlet(quadletPath string) string {
	base := filepath.Base(quadletPath)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)
	return nameWithoutExt + unitServiceSuffix[ext] + ".service"
}

// RelativePath returns the relative path from baseDir to target
func RelativePath(baseDir, target string) (string, error) {
	return filepath.Rel(baseDir, target)
}
