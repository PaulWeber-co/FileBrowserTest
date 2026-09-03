//go:build windows

package vfs

import "golang.org/x/sys/windows"

func diskSpace(path string) (Space, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Space{}, err
	}
	var freeForCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeForCaller, &total, &totalFree); err != nil {
		return Space{}, err
	}
	return Space{Total: int64(total), Free: int64(freeForCaller)}, nil
}
