package flatfile

import "os"

type fileSystem struct {
	replace       func(source, target string) error
	remove        func(path string) error
	syncDirectory func(path string) error
}

func defaultFileSystem() fileSystem {
	return fileSystem{
		replace:       replaceFile,
		remove:        os.Remove,
		syncDirectory: syncDirectory,
	}
}
