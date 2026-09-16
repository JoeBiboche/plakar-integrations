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

package sftp

import (
	"bytes"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

func sshArgs(endpoint *url.URL, params map[string]string) []string {
	// Non-interactive: fail fast instead of hanging on passphrase/host-key prompt
	args := []string{"-o", "BatchMode=yes"}

	if params["insecure_ignore_host_key"] == "true" {
		args = append(args, "-o", "StrictHostKeyChecking=no")
		// args = append(args, "-o", "UserKnownHostsFile=/dev/null") ?
	}

	if id := params["identity"]; id != "" {
		args = append(args, "-i", id)
	}

	if endpoint.User != nil {
		args = append(args, "-l", endpoint.User.Username())
	} else if params["username"] != "" {
		args = append(args, "-l", params["username"])
	}

	if p := endpoint.Port(); p != "" {
		args = append(args, "-p", p)
	}

	return args
}

func dial(args []string) (*sftp.Client, error) {
	cmd := exec.Command("ssh", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// misleading name: Wait() still waits for the process to
	// exit, but then it puts a 2 seconds limit for grandchildren
	// processes to close the pipes etc... that otherwise would
	// block.  think of a proxycommand for example.
	cmd.WaitDelay = 2 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		// ssh should already be dead, but make sure it is
		// anyway.
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("ssh failed: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("ssh failed: %w", err)
	}

	return client, nil
}
