// Package lock prevents competing daemons sharing one local state directory.
package lock

import (
	"fmt"
	"os"
	"syscall"
)

func Acquire(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another daemon owns %s: %w", path, e)
	}
	return f, nil
}
