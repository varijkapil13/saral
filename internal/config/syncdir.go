//go:build !windows

package config

import (
	"errors"
	"os"
	"syscall"
)

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the directory writeAtomic just wrote into
	if err != nil {
		return err
	}
	err = d.Sync()
	// Some filesystems cannot sync a directory and say so; the rename stands.
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		err = nil
	}
	return errors.Join(err, d.Close())
}
