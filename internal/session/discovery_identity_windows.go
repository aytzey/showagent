//go:build windows

package session

import (
	"fmt"
	"os"
	"unsafe"

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
	// FILE_BASIC_INFO requires native eight-byte alignment.
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes                                          uint32
	}
	if windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))) != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", identity.VolumeSerialNumber, identity.FileIndexHigh, identity.FileIndexLow, basic.ChangeTime)
}
