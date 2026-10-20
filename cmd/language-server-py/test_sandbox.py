"""
Sandbox regression tests: known escape techniques must be blocked, and
ordinary handler code must keep working.

Run with: uv run python -m unittest test_sandbox -v
"""

import json
import multiprocessing
import unittest

from executor import execute_and_call


def run(code, input_data=None):
    return json.loads(execute_and_call(code, input_data or {}, 10))


class EscapesAreBlocked(unittest.TestCase):
    def assertBlocked(self, code):
        with self.assertRaises(Exception) as ctx:
            run(code)
        return str(ctx.exception)

    def test_reaching_os_through_an_allowed_module(self):
        msg = self.assertBlocked('''
def handler(input):
    import json
    return json.codecs.sys.modules["os"].getcwd()
''')
        self.assertIn("not allowed", msg)

    def test_from_import_of_a_disallowed_submodule(self):
        self.assertBlocked('''
def handler(input):
    from json import codecs
    return codecs.open("/etc/hosts").read()
''')

    def test_reading_files_outside_python(self):
        self.assertBlocked('''
def handler(input):
    import pathlib
    return pathlib.Path("/etc/hosts").read_text()
''')

    def test_importing_os(self):
        self.assertBlocked('''
def handler(input):
    import os
    return os.getcwd()
''')

    def test_dunder_walk(self):
        self.assertBlocked('''
def handler(input):
    return ().__class__.__bases__[0].__subclasses__()
''')

    def test_str_format_attribute_access(self):
        self.assertBlocked('''
def handler(input):
    return "{0.__class__}".format(1)
''')

    def test_eval_via_attribute(self):
        self.assertBlocked('''
def handler(input):
    import functools
    return functools.reduce.eval
''')

    def test_runaway_loop_times_out(self):
        with self.assertRaises(Exception):
            execute_and_call('''
def handler(input):
    while True:
        pass
''', {}, 1)


class OrdinaryCodeWorks(unittest.TestCase):
    def test_module_level_imports_and_helpers(self):
        out = run('''
import math
from collections import Counter

def helper(x):
    return math.sqrt(x)

def handler(input):
    return {"root": helper(input["n"]), "top": Counter(input["words"]).most_common(1)[0][0]}
''', {"n": 16, "words": ["a", "b", "a"]})
        self.assertEqual(out, {"root": 4.0, "top": "a"})

    def test_stdlib_data_handling(self):
        out = run('''
import json, datetime, re, statistics
def handler(input):
    d = datetime.date(2026, 1, 2)
    return {
        "mean": statistics.mean(input["xs"]),
        "iso": d.isoformat(),
        "digits": re.findall(r"\\d+", "a1b22"),
        "roundtrip": json.loads(json.dumps({"k": 1})),
    }
''', {"xs": [1, 2, 3]})
        self.assertEqual(out["mean"], 2)
        self.assertEqual(out["digits"], ["1", "22"])

    def test_hasattr_still_works(self):
        out = run('''
def handler(input):
    return {"has_keys": hasattr(input, "keys"), "has_dunder": hasattr(input, "__class__")}
''')
        self.assertEqual(out, {"has_keys": True, "has_dunder": False})


def _guarded_attempt(action, conn):
    """Child process: install only the interpreter-level guard, then try action."""
    import guard
    guard.install()
    try:
        action()
        conn.send("allowed")
    except PermissionError as e:
        conn.send(f"blocked: {e}")


def _read_etc_hosts():
    open("/etc/hosts").read()


def _write_tmp_file():
    open("/tmp/composer-sandbox-probe", "w").write("x")


def _open_socket():
    import socket
    socket.create_connection(("127.0.0.1", 9), timeout=1)


def _run_shell():
    import os
    os.system("true")


def _read_python_stdlib():
    import os
    open(os.__file__).read(10)


class InterpreterGuard(unittest.TestCase):
    """The audit-hook layer must hold even if name-level guards are bypassed."""

    def attempt(self, action):
        parent, child = multiprocessing.Pipe(duplex=False)
        proc = multiprocessing.Process(target=_guarded_attempt, args=(action, child))
        proc.start()
        proc.join(10)
        return parent.recv()

    def test_blocks_dangerous_operations(self):
        for action in (_read_etc_hosts, _write_tmp_file, _open_socket, _run_shell):
            with self.subTest(action=action.__name__):
                self.assertTrue(self.attempt(action).startswith("blocked"))

    def test_allows_reading_the_python_installation(self):
        self.assertEqual(self.attempt(_read_python_stdlib), "allowed")


if __name__ == "__main__":
    unittest.main()
