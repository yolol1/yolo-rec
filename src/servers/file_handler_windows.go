//go:build windows

package servers

import (
	"os"

	"golang.org/x/sys/windows"
)

func isPathReparsePoint(path string, _ os.FileInfo) (bool, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return false, err
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
