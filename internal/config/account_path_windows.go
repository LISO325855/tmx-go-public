package config

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks does not resolve Windows directory junctions. Ask the filesystem
// for the path of an open handle, which resolves all reparse points and 8.3 names.
func finalAccountPath(path string) (string, error) {
	h, err := openAccountPath(path)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if n >= uint32(len(buf)) {
		return "", windows.ERROR_FILENAME_EXCED_RANGE
	}
	resolved := windows.UTF16ToString(buf[:n])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(resolved, `\\?\`), nil
}

func openAccountPath(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

// Some Windows RAM disks expose volume identity but cannot produce a final
// DOS path. Compare the volumes of the opened targets, following redirects.
func distinctAccountVolumes(account, temp string, resolveErr error) bool {
	if !errors.Is(resolveErr, windows.ERROR_INVALID_FUNCTION) {
		return false
	}
	volume := func(path string) uint32 {
		h, err := openAccountPath(path)
		if err != nil {
			return 0
		}
		defer windows.CloseHandle(h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return 0
		}
		return info.VolumeSerialNumber
	}
	a, b := volume(account), volume(temp)
	return a != 0 && b != 0 && a != b
}
