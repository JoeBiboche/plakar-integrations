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

// Windows never reuses a ControlMaster: there is no unix-domain
// control socket to multiplex through (net.Dial("unix", ...), used by
// checkMaster in connect_unix.go, has no Windows equivalent for a
// process-shared, filesystem-permission-protected socket). So every
// call to connect() on Windows spawns its own ssh process instead of
// sharing one behind a control socket directory. See README.md for
// the full rationale and history.
package sftp

import (
	"fmt"
	"net/url"

	"github.com/pkg/sftp"
)

func checkParamSupportForWindows(params map[string]string) error {
	for _, key := range []string{"ssh_auth_sock", "ssh_private_key", "ssh_private_key_ttl"} {
		if val := params[key]; val != "" {
			return fmt.Errorf("%q not supported on Windows", key)
		}
	}

	return nil
}

func connect(endpoint *url.URL, params map[string]string) (*sftp.Client, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("nil endpoint")
	}

	host := endpoint.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing hostname in endpoint: %q", endpoint.String())
	}

	if err := checkParamSupportForWindows(params); err != nil {
		return nil, err
	}

	args := sshArgs(endpoint, params)
	args = append(args, "-s", "--", host, "sftp")

	return dial(args)
}
