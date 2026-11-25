//go:build windows

package codexguard

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func shimGeneration(p *Pin) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(p.file.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x-%08x%08x-%s", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, p.SHA256[:16]), nil
}

func openPinned(path string, shareDelete bool) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	share := uint32(windows.FILE_SHARE_READ)
	if shareDelete {
		share |= windows.FILE_SHARE_DELETE
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
