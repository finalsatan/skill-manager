//go:build windows

package link

import (
	"io/fs"
	"os"
	"strings"

	junction "github.com/nyaosorg/go-windows-junction"
)

const junctionSupported = true

// readLink handles both symlinks and junctions. Since Go 1.23 a junction is
// reported as ModeIrregular rather than ModeSymlink, but os.Readlink still
// resolves it.
func readLink(path string, fi fs.FileInfo) (string, bool) {
	if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		return "", false
	}
	dest, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	for _, p := range []string{`\\?\UNC\`, `\??\UNC\`} {
		if strings.HasPrefix(dest, p) {
			return `\\` + dest[len(p):], true
		}
	}
	for _, p := range []string{`\\?\`, `\??\`} {
		dest = strings.TrimPrefix(dest, p)
	}
	return dest, true
}

func createJunction(src, dst string) error {
	return junction.Create(src, dst)
}
