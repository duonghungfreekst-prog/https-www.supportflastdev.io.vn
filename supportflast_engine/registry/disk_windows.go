//go:build windows

package registry

import (
	"syscall"
	"unsafe"
)

// getDiskSpace lấy tổng dung lượng và dung lượng còn trống trên Windows qua Windows API
func getDiskSpace(path string) (totalBytes, freeBytes uint64, err error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64

	r1, _, errCall := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return 0, 0, errCall
	}

	return totalNumberOfBytes, freeBytesAvailable, nil
}
