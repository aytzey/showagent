package session

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openTrimWriter(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(trimWindowsPath(path))
	if err != nil {
		return nil, err
	}
	// Share reads and replacement, but reject an existing writable handle
	// and prevent native clients from reopening the old inode for writing.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// MoveFileEx cannot replace an open destination. POSIX rename semantics keep
// the old handle valid while swapping the directory entry in one operation.
func replaceTrimFile(from, to string) error {
	name, err := windows.UTF16PtrFromString(trimWindowsPath(from))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	wide, err := windows.UTF16FromString(trimWindowsPath(to))
	if err != nil {
		return err
	}
	type renameInfo struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	const replaceExisting, posixSemantics = 1, 2
	nameLength := len(wide) - 1
	data := make([]byte, int(unsafe.Offsetof(renameInfo{}.FileName))+2*len(wide))
	info := (*renameInfo)(unsafe.Pointer(unsafe.SliceData(data)))
	info.Flags = replaceExisting | posixSemantics
	info.FileNameLength = uint32(2 * nameLength)
	copy(unsafe.Slice(&info.FileName[0], len(wide)), wide)
	return windows.SetFileInformationByHandle(handle, windows.FileRenameInfoEx, unsafe.SliceData(data), uint32(len(data)))
}

func trimWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
