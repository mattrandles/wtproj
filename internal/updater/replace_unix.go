//go:build !windows

package updater

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceExecutable(source, target string) (bool, error) {
	if err := os.Rename(source, target); err != nil {
		return false, err
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return false, fmt.Errorf("replacement completed but open parent directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return false, fmt.Errorf("replacement completed but sync parent directory: %w", err)
	}
	return false, nil
}
