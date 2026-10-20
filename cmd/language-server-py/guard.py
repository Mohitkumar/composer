"""
Capability guard for sandboxed code, built on PEP 578 audit hooks.

RestrictedPython controls what user code can *name*; this guard controls what
the interpreter will *do*, regardless of how the code got there (a library
function, a reachable module attribute, a bug in the name-level guards).
CPython raises an audit event before every sensitive operation, and a hook that
raises blocks it. Once installed, an audit hook cannot be removed.

Policy for the sandbox child process:
- files: read-only, and only inside the Python installation (so lazy imports
  and package data work) plus timezone data; every write is denied
- no network, no subprocesses, no signals, no filesystem mutation
- no ctypes, no hand-built code or function objects, no pickle loading
"""

import os
import site
import sys

# Event names denied outright, or by prefix.
_DENIED_PREFIXES = (
    "socket.",
    "subprocess.",
    "shutil.",
    "ctypes.",
    "ftplib.",
    "smtplib.",
    "telnetlib.",
    "poplib.",
    "imaplib.",
    "nntplib.",
    "http.client.",
    "os.exec",
    "os.spawn",
    "os.posix_spawn",
    "os.fork",
    "os.kill",
    "winreg.",
)
_DENIED_EVENTS = frozenset({
    "os.system", "os.startfile", "os.putenv", "os.unsetenv",
    "os.remove", "os.rename", "os.rmdir", "os.mkdir", "os.chmod", "os.chown",
    "os.chflags", "os.link", "os.symlink", "os.truncate", "os.utime",
    "os.add_dll_directory", "os.setxattr", "os.removexattr",
    "pty.spawn", "urllib.Request", "webbrowser.open",
    "code.__new__", "function.__new__",
    "pickle.find_class",
    "builtins.input", "builtins.breakpoint",
    "sys.settrace", "sys.setprofile", "sys.monitoring.register_callback",
})

_WRITE_FLAGS = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREAT | os.O_TRUNC

_installed = False


class SandboxViolation(PermissionError):
    """Raised when sandboxed code attempts a denied operation."""


def install(extra_read_roots=()):
    """Install the guard in the current process. Idempotent."""
    global _installed
    if _installed:
        return
    roots = _read_roots(extra_read_roots)

    def hook(event, args):
        if event == "open":
            _check_open(args, roots)
        elif event in ("os.listdir", "os.scandir", "glob.glob"):
            if args and args[0] is not None:
                _check_read(args[0], roots, event)
        elif event in _DENIED_EVENTS or event.startswith(_DENIED_PREFIXES):
            raise SandboxViolation(f"sandbox: {event} is not allowed")

    sys.addaudithook(hook)
    _installed = True


def _read_roots(extra):
    roots = {sys.prefix, sys.base_prefix, sys.exec_prefix, sys.base_exec_prefix,
             "/usr/share/zoneinfo", "/etc/localtime"}
    try:
        roots.update(site.getsitepackages())
    except AttributeError:  # some virtualenv layouts
        pass
    roots.update(extra)
    return tuple(sorted({os.path.realpath(r) for r in roots if r}))


def _check_open(args, roots):
    path, mode, flags = (tuple(args) + (None, None, None))[:3]
    if isinstance(path, int):
        return  # an already-open descriptor; creating it was checked
    if isinstance(mode, str):
        writing = any(c in mode for c in "wax+")
    else:
        writing = isinstance(flags, int) and bool(flags & _WRITE_FLAGS)
    if writing:
        raise SandboxViolation("sandbox: writing files is not allowed")
    _check_read(path, roots, "open")


def _check_read(path, roots, event):
    try:
        real = os.path.realpath(os.fsdecode(path))
    except (TypeError, ValueError):
        raise SandboxViolation(f"sandbox: {event} of this path is not allowed")
    for root in roots:
        if real == root or real.startswith(root + os.sep):
            return
    raise SandboxViolation(f"sandbox: reading {real} is not allowed")
