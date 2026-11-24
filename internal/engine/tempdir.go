package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// All temporary files and directories of a running plugin live under one
// per-instance directory, so a plugin that was killed mid-conversion leaves
// nothing the next start cannot identify as its own. Until SetTempDir is
// called (library use, tests) the system temp directory is used.
var tempRoot atomic.Value // string

// SetTempDir makes dir the parent of every temporary file the engine creates.
func SetTempDir(dir string) { tempRoot.Store(dir) }

// TempDir returns the directory for temporary files ("" = system default),
// creating it when it was removed.
func TempDir() string {
	dir, _ := tempRoot.Load().(string)
	if dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	return dir
}

// userTag names the current user in the temp root: the numeric uid, or the
// sanitised user name where there is no uid (Windows).
func userTag() string {
	if uid := os.Getuid(); uid >= 0 {
		return strconv.Itoa(uid)
	}
	name := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}

// InstanceTempDir is the temp directory of the plugin listening on port:
// <temp dir>/gleann-plugin-docs-<user>/<port>. The per-user root keeps another
// local user from pre-creating the shared parent.
func InstanceTempDir(port int) string {
	return filepath.Join(os.TempDir(), "gleann-plugin-docs-"+userTag(), strconv.Itoa(port))
}

// checkPrivateDir refuses a directory that is a symlink, is not a directory,
// belongs to someone else or is writable by group or others.
func checkPrivateDir(dir string, allowSticky bool) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link", dir)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case !ownedByCurrentUser(fi) && !allowSticky:
		return fmt.Errorf("%s belongs to another user", dir)
	case writableByOthers(fi, allowSticky):
		return fmt.Errorf("%s is writable by other users", dir)
	}
	return nil
}

// ensurePrivateDir creates dir (mode 0700) when it does not exist and then
// applies checkPrivateDir.
func ensurePrivateDir(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return checkPrivateDir(dir, false)
}

// ResetTempDir empties dir (removing what a previous instance left behind) and
// recreates it. dir is <root>/<port>; the root must be a private directory of
// the current user (not a symlink, not owned by someone else, not writable by
// others), otherwise nothing is touched and an error is returned.
func ResetTempDir(dir string) error {
	root := filepath.Dir(dir)
	if err := ensurePrivateDir(root); err != nil {
		return fmt.Errorf("temp root: %w", err)
	}
	// RemoveAll does not follow symlinks: a link in place of dir is removed itself.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	return checkPrivateDir(dir, false)
}

// legacyTempPrefixes are the names earlier versions created directly in the
// system temp directory. gleann_upload_* is deliberately not listed: the
// gleann server uses the same prefix for its own uploads.
var legacyTempPrefixes = []string{"gleann-img-", "gleann_deep_", "gleann_docling_pdf_", "gleann-legacy-"}

// SweepLegacyTemp removes entries of root that match an earlier version's temp
// names, are regular files or directories (never symlinks), belong to the
// current user and have not been modified for olderThan. It returns the number
// removed. A root that is a symlink, belongs to someone else without being the
// shared sticky temp directory, or is writable by others without the sticky
// bit is refused: nothing is removed.
func SweepLegacyTemp(root string, olderThan time.Duration) int {
	if err := checkSweepRoot(root); err != nil {
		log.Printf("[docling-eif] not sweeping %s: %v", root, err)
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, ent := range entries {
		match := false
		for _, p := range legacyTempPrefixes {
			if strings.HasPrefix(ent.Name(), p) {
				match = true
				break
			}
		}
		if !match || !(ent.Type().IsRegular() || ent.Type().IsDir()) {
			continue
		}
		info, err := ent.Info() // lstat: never follows a link
		if err != nil || !ownedByCurrentUser(info) || time.Since(info.ModTime()) < olderThan {
			continue
		}
		if os.RemoveAll(filepath.Join(root, ent.Name())) == nil {
			removed++
		}
	}
	return removed
}

// checkSweepRoot accepts a private directory of the user or a sticky shared
// directory such as /tmp (where the entry-level owner check protects others).
func checkSweepRoot(root string) error {
	fi, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s is not a plain directory", root)
	}
	if ownedByCurrentUser(fi) {
		return checkPrivateDir(root, true)
	}
	if fi.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("%s belongs to another user and is not a sticky directory", root)
	}
	return nil
}
