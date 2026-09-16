//go:build !windows

/*
 * Copyright (c) 2025 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

// This file holds the unix implementation of connect(), including the
// ssh ControlMaster machinery: a shared "master" ssh process is kept
// alive per (endpoint, username, identity) behind a unix-domain
// control socket, and later connections multiplex through it with
// `ssh -S <sock>` instead of paying for a fresh TCP handshake and
// auth round-trip every time.
//
// It is built only for !windows because there is no Windows
// equivalent: see connect_windows.go and README.md for the full
// rationale. Keeping this behind a build tag (rather than a
// runtime.GOOS check in shared code) means the control socket
// directory, its private-permissions check, and this file's
// flock-based locking are entirely absent from Windows builds
// instead of being dead code that compiles but never runs there.
package sftp

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

func controlDir() (string, error) {
	var base string

	if run := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(run) {
		base = run
	} else if cache, err := os.UserCacheDir(); err != nil {
		return "", fmt.Errorf("cannot locate a private cache directory: %w", err)
	} else {
		base = cache
	}

	dir := filepath.Join(base, "plakar", "ssh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	// MkdirAll is happy with a directory that already exists, whoever owns
	// it and whatever its mode is, so check what we ended up with.
	if err := checkPrivateDir(dir); err != nil {
		return "", err
	}

	return dir, nil
}

func controlSock(endpoint *url.URL, params map[string]string) (string, error) {
	dir, err := controlDir()
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("%s|%s|%s", endpoint.String(), params["username"],
		params["identity"])
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.sock", sum[:8])), nil
}

func setupPrivateKey(params map[string]string) error {
	key := params["ssh_private_key"]
	if key == "" {
		return nil
	}

	ttl := params["ssh_private_key_ttl"]
	if ttl == "" {
		ttl = "5s"
	}

	cmd := exec.Command("ssh-add", "-t", ttl, "-")
	if sshAuthSock := params["ssh_auth_sock"]; sshAuthSock != "" {
		cmd.Env = append(cmd.Environ(), "SSH_AUTH_SOCK="+sshAuthSock)
	}

	cmd.Stdin = strings.NewReader(key + "\n")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add key: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func checkMaster(sock string) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return fmt.Errorf("ssh master not up; failed to connect: %w", err)
	}
	conn.Close()
	return nil
}

func startMaster(endpoint *url.URL, params map[string]string, host, sock string) error {
	args := sshArgs(endpoint, params)
	args = append(args,
		"-N", "-f", "-S", sock,
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=10m",
		"--", host,
	)

	cmd := exec.Command("ssh", args...)
	if sshAuthSock := params["ssh_auth_sock"]; sshAuthSock != "" {
		cmd.Env = append(cmd.Environ(), "SSH_AUTH_SOCK="+sshAuthSock)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start ssh master: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureMaster(endpoint *url.URL, params map[string]string, host string) (string, error) {
	sock, err := controlSock(endpoint, params)
	if err != nil {
		return "", err
	}

	if err := checkMaster(sock); err == nil {
		return sock, nil
	}

	// we can't safely remove this without introducing races with
	// clients attempting to spawn the master.
	lockfile, err := flock(sock + ".lock")
	if err != nil {
		return "", err
	}

	defer lockfile.Close()

	var spawned bool
	for range 100 {
		time.Sleep(10 * time.Millisecond)

		// always retry at least once after we've got the
		// lock, because another process could have taken it,
		// started the server and released it between our
		// checkMaster() and flock().
		if err := checkMaster(sock); err == nil {
			return sock, nil
		}

		if !spawned {
			spawned = true

			// add the private key to the agent if necessary
			if err := setupPrivateKey(params); err != nil {
				return "", fmt.Errorf("failed to set private key: %w", err)
			}

			os.Remove(sock)
			if err := startMaster(endpoint, params, host, sock); err != nil {
				return "", err
			}
		}
	}
	return "", fmt.Errorf("ssh master failed to come up")
}

func connect(endpoint *url.URL, params map[string]string) (*sftp.Client, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("nil endpoint")
	}

	host := endpoint.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing hostname in endpoint: %q", endpoint.String())
	}

	// ensure the master exists (idempotent) and get the control socket path.
	sock, err := ensureMaster(endpoint, params, host)
	if err != nil {
		return nil, err
	}

	// reuse the master
	args := sshArgs(endpoint, params)
	args = append(args, "-S", sock, "-s", "--", host, "sftp")

	return dial(args)
}
