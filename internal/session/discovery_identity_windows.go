//go:build windows

package session

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func discoveryFileIdentity(path string, _ os.FileInfo) string {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var identity windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &identity) != nil {
		return ""
	}
	// FILE_BASIC_INFO has four 64-bit timestamps; ChangeTime is the fourth.
	var basic [40]byte
	if windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, &basic[0], uint32(len(basic))) != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", identity.VolumeSerialNumber, identity.FileIndexHigh, identity.FileIndexLow, binary.LittleEndian.Uint64(basic[24:32]))
}
