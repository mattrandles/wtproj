//go:build !windows

package flatfile

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func normalizePathForComparison(path string) string {
	return path
}
