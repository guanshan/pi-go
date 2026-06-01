//go:build windows

package tools

import "golang.org/x/sys/windows"

func replaceFile(oldpath, newpath string) error {
	return windows.Rename(oldpath, newpath)
}
