//go:build unix

package engine

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the file belongs to the user running the
// process.
func ownedByCurrentUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// writableByOthers reports whether group or world can write to the directory.
// A sticky directory (the shared system temp directory) is acceptable when
// allowSticky is set, because others cannot touch entries they do not own.
func writableByOthers(fi fs.FileInfo, allowSticky bool) bool {
	m := fi.Mode()
	if m&0o022 == 0 {
		return false
	}
	return !(allowSticky && m&os.ModeSticky != 0)
}
