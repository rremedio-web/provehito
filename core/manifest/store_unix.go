//go:build darwin || linux

package manifest

import (
	"errors"
	"os"
	"syscall"
)

// manifestLock serializes changes to one manifest path. The lock path is a
// literal sibling name so manifest basenames are never interpreted as patterns.
type manifestLock struct {
	file *os.File
}

func lockManifest(manifestPath string) (*manifestLock, error) {
	lockPath := manifestPath + ".lock"
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("manifest lock path is a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &manifestLock{file: file}, nil
}

func (lock *manifestLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// noFollowFlag is kept in one place so opening a target cannot follow a
// symlink after the preceding Lstat check.
func noFollowFlag() int {
	return syscall.O_NOFOLLOW
}
