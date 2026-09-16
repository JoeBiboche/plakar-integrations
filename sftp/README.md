# SFTP Integration

## Overview

**SFTP (SSH File Transfer Protocol)** is a secure file transfer protocol that operates over SSH, providing encrypted access to files on remote servers.
It is commonly used in server administration, enterprise storage solutions, and legacy systems for file exchange.

This integration allows:

* **Seamless backup of files hosted on SFTP servers into a Kloset repository:**
  Capture and store entire directory trees or specific remote paths from SFTP servers with full metadata, enabling secure and consistent backups across environments.

* **Direct restoration of snapshots to remote SFTP destinations:**
  Restore previously backed-up snapshots directly to SFTP servers, preserving file hierarchies, permissions, and timestamps.

* **Compatibility with secure remote environments and legacy systems:**
  Supports a wide range of remote servers, from modern cloud-hosted Linux instances to older on-premise infrastructure relying on SSH-based access.

---

## Configuration

The supported configuration options are:

* `location`: remote server hostname or IP

It relies on the `sftp` executable and will use the user-configuration for additional options.

---

## How connections are established

This integration doesn't speak SSH itself: it shells out to the system's
`ssh` binary (via `os/exec`) and talks SFTP over its stdin/stdout, so it
inherits whatever `~/.ssh/config`, agent, and known_hosts setup the user
already has. The two platforms behave quite differently, and that
difference is intentional rather than an oversight, so it's worth
understanding before touching this code.

### Unix: one shared "master" connection per destination

Every `ssh` invocation pays for a fresh TCP handshake and authentication
round-trip, which is slow when a backup or restore issues many SFTP
operations against the same host. On unix, we avoid that by using OpenSSH's
`ControlMaster` feature:

1. The first connection to a given `(endpoint, username, identity)` starts a
   detached `ssh -M -N -f -S <sock> -o ControlMaster=yes ...` process — the
   "master" — and leaves it running (`ControlPersist=10m`).
2. That master listens on a unix-domain socket (`<sock>`). Every later
   connection to the same destination reuses it with `ssh -S <sock> ...`
   instead of authenticating again.
3. Concurrent callers race to become the one that starts the master, so
   spawning is guarded by a filesystem lock (`flock` on `<sock>.lock`) — see
   `ensureMaster` in `connect_unix.go`.

This is all implemented in **`connect_unix.go`**, built only for `!windows`.

**Why the control socket directory is checked, not just created.** The
socket path is derived from a SHA-256 of the endpoint/username/identity
(`controlSock`), and lives under `$XDG_RUNTIME_DIR/plakar/ssh` (or the OS
user cache dir as a fallback). That path is predictable and, on a
multi-user machine, `os.MkdirAll` will happily succeed even if the
directory already exists and is owned by someone else or world-writable.
If we didn't verify that, another local user could pre-create the
directory (or the socket inside it) and have our `ssh -O check` / SFTP
traffic multiplexed through *their* process instead of a fresh one we
control — reading backup data or injecting restore data. `checkPrivateDir`
(`osdep_unix.go`) closes that gap by requiring the resolved directory to be
owned by the current uid and mode `0700` (no group/other access) before
anything is written into it.

### Windows: no multiplexing, one `ssh` process per connection

`connect_windows.go` spawns a plain `ssh -s -- <host> sftp` per connection
and returns — there is no master process, no control socket, and no
`ensureMaster`/`controlDir` equivalent. This isn't a partial port; it's
deliberate, and has been true since the SFTP integration first ran on
Windows (`Fix #54: remove dependency on SSH multiplexing and the auth
socket not supported on Windows`), because `ControlMaster` depends on a
unix-domain socket that Windows OpenSSH doesn't expose the same way, and
the auth-agent forwarding path (`ssh_auth_sock`) doesn't apply there
either. Accordingly, `checkParamSupportForWindows` rejects the
`ssh_auth_sock`, `ssh_private_key`, and `ssh_private_key_ttl` parameters
outright on Windows rather than silently ignoring them.

One consequence worth knowing if you're touching this code: because
Windows never calls `controlDir`/`checkPrivateDir`, there is **no Windows
implementation of the private-directory ACL check**, and there shouldn't
be one added speculatively — it would have no caller and no way to be
exercised or tested. If Windows control-socket reuse is ever implemented,
whoever adds it will need a real access-control check alongside it: unlike
Unix, Windows has no owner/mode bits, so verifying "private to the current
user" means inspecting the directory's security descriptor (owner SID and
DACL, e.g. via `golang.org/x/sys/windows`) and rejecting any ACE broader
than the current user plus `SYSTEM`/`Administrators` — a plain
"is-it-a-directory" check would not be an equivalent guard.

### File layout

| File | Platform | Contents |
|---|---|---|
| `connect.go` | both | `sshArgs` (shared ssh flag building), `dial` (spawn `ssh`, speak SFTP over its pipes) |
| `connect_unix.go` | `!windows` | `connect`, `ensureMaster`, `controlDir`/`controlSock`, `checkMaster`/`startMaster`, `setupPrivateKey` |
| `connect_windows.go` | `windows` | `connect`, `checkParamSupportForWindows` |
| `osdep_unix.go` | `!windows` | `flock`, `checkPrivateDir` (owner + mode check) |

Splitting `connect_unix.go`/`connect_windows.go` by build tag (rather than
branching on `runtime.GOOS` inside one shared `connect()`) means the
ControlMaster/control-socket machinery is entirely absent from Windows
binaries, instead of being compiled in as code that happens to never run
there.

---

## Examples

```sh
# back up a remote path over SFTP
$ plakar backup sftp://user@host/var/www

# restore the snapshot "abc" to an SFTP server
$ plakar restore -to sftp://user@host/var/www abc

# create a kloset repository on a remote SFTP server
$ plakar at sftp://user@host/backups create
```
