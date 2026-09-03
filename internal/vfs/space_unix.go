//go:build !windows

package vfs

import "golang.org/x/sys/unix"

func diskSpace(path string) (Space, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Space{}, err
	}
	bs := int64(st.Bsize)
	return Space{
		Total: int64(st.Blocks) * bs,
		Free:  int64(st.Bavail) * bs,
	}, nil
}
