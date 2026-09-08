# Unix socket ownership

Listeners use a persistent `socket-path.lock` regular file and an exclusive
`flock` while inspecting, removing or creating the socket path. Startup and
cleanup follow the same rule, so cooperating processes cannot unlink a socket
created by another owner between an identity check and removal. The lock is
released after each ownership transition; it is not held for the listener's
lifetime.

The companion file is created with mode 0600 and is never truncated or removed.
Existing contents are preserved. Symlinks, directories and other nonregular
lock entries are rejected, and the opened inode is checked again after taking
the lock. Removing the lock file while listeners are running would allow two
independent lock inodes and defeats serialization.

The parent directory must remain trusted. Processes that replace paths without
using the companion lock, or can replace ancestor directories, are outside this
cooperating ownership protocol. Cleanup still checks socket identity and leaves
an already installed replacement untouched. Error cleanup inside `Listen` uses
the lock already held rather than recursively acquiring it through `Close`.

Locking is implemented on Darwin, DragonFly BSD, FreeBSD, Linux, NetBSD and
OpenBSD. Other targets compile but `Listen` returns an explicit unsupported
platform error before creating files. This leaves TCP control-server builds
available without providing an unlocked Unix-socket fallback.
