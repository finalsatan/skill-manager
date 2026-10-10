//go:build !windows

package link

import (
	"errors"
	"io/fs"
	"os"
)

const junctionSupported = false

func readLink(path string, fi fs.FileInfo) (string, bool) {
	if fi.Mode()&fs.ModeSymlink == 0 {
		return "", false
	}
	dest, err := os.Readlink(path)
	return dest, err == nil
}

func createJunction(src, dst string) error {
	return errors.New("junctions are only available on Windows")
}
