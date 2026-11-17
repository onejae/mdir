#!/usr/bin/env python3
"""Exercise the real mdir-fm binary in a PTY using only disposable fixtures."""

import argparse
import errno
import fcntl
import os
from pathlib import Path
import pty
import select
import signal
import struct
import sys
import tempfile
import termios
import time
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", nargs="?", default="bin/mdir-fm")
    parser.add_argument("--capture", help="optional ANSI capture path")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    output = bytearray()

    with tempfile.TemporaryDirectory(prefix="mdir-smoke-") as temporary:
        base = Path(temporary)
        root = base / "fixture"
        root.mkdir()
        (root / "folder").mkdir()
        (root / "alpha.txt").write_text("first line\nsecond line\nlast line\n")
        (root / "한글 문서.txt").write_text("안녕하세요\n")
        config = base / "config.json"
        config.write_text("{}")
        editor = base / "editor.sh"
        editor.write_text('#!/bin/sh\nprintf "edited\\n" >> "$1"\n')
        editor.chmod(0o700)
        with zipfile.ZipFile(root / "bundle.zip", "w") as archive:
            archive.writestr("nested/archive-note.txt", "archive content\n")
        last_dir = base / "last-dir"

        child, master = pty.fork()
        if child == 0:
            environment = os.environ.copy()
            environment.pop("NO_COLOR", None)
            environment.pop("TCELL_TRUECOLOR", None)
            environment.update(
                TERM="xterm-256color",
                COLORTERM="truecolor",
                LANG="en_US.UTF-8" if sys.platform == "darwin" else "C.UTF-8",
                LC_ALL="en_US.UTF-8" if sys.platform == "darwin" else "C.UTF-8",
                XDG_CONFIG_HOME=str(base / "config"),
                XDG_STATE_HOME=str(base / "state"),
                VISUAL=str(editor),
            )
            os.execve(binary, [binary, "-config", str(config), "-last-dir", str(last_dir), str(root)], environment)

        reaped = False

        def resize(columns, rows):
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))

        def drain(seconds=0.12):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                if not select.select([master], [], [], max(0, deadline - time.monotonic()))[0]:
                    break
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not chunk:
                    break
                output.extend(chunk)

        def wait_for(predicate, label):
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                drain(0.05)
                if predicate():
                    return
            raise AssertionError("Timed out: " + label)

        def send(text):
            os.write(master, text.encode())
            drain()

        def find(name):
            send("/\x15" + name + "\r")

        try:
            resize(100, 28)
            banner = b"MDIR / MACOS" if sys.platform == "darwin" else b"MDIR / LINUX"
            wait_for(lambda: banner in output, "initial screen")
            # tcell positions wide glyphs individually, so they need not be adjacent in the byte stream.
            assert all(char.encode() in output for char in "한글"), "Unicode filename missing"
            assert b"48;2;0;170;170" in output, "DOS cyan RGB background missing"
            if args.capture:
                Path(args.capture + ".initial").write_bytes(output)
            send(":mkdir new folder\r")
            wait_for(lambda: (root / "new folder").is_dir(), "mkdir")
            find("alpha.txt")
            send(":copy copy.txt\r")
            wait_for(lambda: (root / "copy.txt").exists(), "copy")
            assert (root / "copy.txt").read_text() == (root / "alpha.txt").read_text()
            find("alpha.txt")
            send(":rename renamed.txt\r")
            wait_for(lambda: (root / "renamed.txt").exists(), "rename")
            assert not (root / "alpha.txt").exists()
            find("renamed.txt")
            send(":view\r")
            wait_for(lambda: b"Mdir viewer" in output, "viewer")
            send("jGggHq")
            find("renamed.txt")
            send("e")
            wait_for(lambda: "edited" in (root / "renamed.txt").read_text(), "external editor")
            send(":move folder/moved.txt\r")
            wait_for(lambda: (root / "folder" / "moved.txt").exists(), "move")
            # Keep the captured tree viewport inside our disposable fixture.
            for index in range(40):
                (root / f"example-{index:02d}").mkdir()
            send("t")
            wait_for(lambda: b"Change Directory" in output, "tree")
            send("j" * 35)
            if args.capture:
                Path(args.capture + ".tree").write_bytes(output)
            send("q")
            find("copy.txt")
            send("x\r")  # Default confirmation is cancel.
            assert (root / "copy.txt").exists(), "default delete confirmation removed file"
            send("xj\r")
            wait_for(lambda: not (root / "copy.txt").exists(), "confirmed delete")
            find("bundle.zip")
            send("l")
            send(":extract-all extracted\r")
            wait_for(lambda: (root / "extracted" / "nested" / "archive-note.txt").exists(), "archive extraction")
            send("h")
            send(":!printf 'mdir-shell-check'\r")
            wait_for(lambda: b"Press Enter to return" in output, "shell handoff")
            send("\r")
            resize(40, 12)
            drain()
            resize(120, 32)
            drain()
            send("ggq")

            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                drain(0.05)
                pid, status = os.waitpid(child, os.WNOHANG)
                if pid:
                    reaped = True
                    assert os.waitstatus_to_exitcode(status) == 0, "binary exited unsuccessfully"
                    break
            assert reaped, "binary did not quit"
            assert Path(last_dir.read_text().strip()).resolve() == root.resolve(), "last-directory output wrong"
            assert (base / "state" / "mdir" / "state.json").exists(), "history not saved"
            print("PASS: real PTY startup, Unicode, mkdir/copy/rename/move, viewer, editor, tree, delete confirmation, archive extraction, shell, resize, quit")
        finally:
            if args.capture:
                Path(args.capture).write_bytes(output)
            if not reaped:
                try:
                    os.kill(child, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os.waitpid(child, 0)
            os.close(master)


if __name__ == "__main__":
    main()
