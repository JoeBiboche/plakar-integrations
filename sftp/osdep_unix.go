//go:build !windows

package sftp

import (
	"fmt"
	"os"
	"syscall"
)

func flock(p string) (*os.File, error) {
	fp, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", p, err)
	}

	for {
		err = syscall.Flock(int(fp.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			fp.Close()
			return nil, err
		}

		break
	}

	return fp, nil
}

// checkPrivateDir verifies that dir is a directory owned by the
// current user and not accessible to anyone else, so that a
// unix-domain control socket created inside it can't be raced or
// squatted by another local user (see connect_unix.go and README.md
// for what this guards).
//
// There is no Windows equivalent of this function: nothing on
// Windows creates or trusts a control-socket directory (see the
// runtime.GOOS branch in connect.go), so a Windows port would have
// no caller. If Windows ever gains ControlMaster-style socket reuse,
// this check would need a real port, not just a directory-exists
// check: Windows has no owner/mode bits, so it would need to inspect
// the security descriptor's owner SID and DACL via
// golang.org/x/sys/windows and reject anything broader than the
// current user (plus SYSTEM/Administrators).
//
// Caveat even on unix: this only looks at the traditional owner/mode
// bits, not POSIX ACLs. A directory with mode 0700 but an extra
// setfacl(1) entry granting another user access would still pass.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	if fi.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%s is accessible to other users (mode %o)", dir, fi.Mode().Perm())
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uint64(st.Uid) != uint64(os.Getuid()) {
		return fmt.Errorf("%s is not owned by uid %d", dir, os.Getuid())
	}

	return nil
}
