//go:build windows

package flatfile

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(source, target string) error {
	sourceUTF16, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return &os.PathError{Op: "replace", Path: source, Err: err}
	}
	targetUTF16, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return &os.PathError{Op: "replace", Path: target, Err: err}
	}

	result, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(sourceUTF16)),
		uintptr(unsafe.Pointer(targetUTF16)),
		uintptr(moveFileReplaceExisting|moveFileWriteThrough),
	)
	if result == 0 {
		if callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return &os.PathError{Op: "replace", Path: target, Err: callErr}
	}
	return nil
}

// MoveFileExW with MOVEFILE_WRITE_THROUGH makes the same-volume replacement
// durable. Windows does not expose directory handles through os.File.Sync.
func syncDirectory(string) error {
	return nil
}

func normalizePathForComparison(path string) string {
	return strings.ToLower(path)
}
