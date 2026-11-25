//go:build !windows

package accounts

import "os"

func syncResetDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func renameReset(from, to string) error { return os.Rename(from, to) }
