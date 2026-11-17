#!/usr/bin/env python3
"""Generate the Homebrew formula from a published mdir release tag."""

import argparse
import hashlib
import io
from pathlib import Path
import re
import sys
import tarfile
import tempfile
from urllib.error import URLError
from urllib.request import urlopen


def release_tag(value):
    if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", value):
        raise argparse.ArgumentTypeError("use a stable release tag such as v0.1.0")
    return value


def render_formula(tag, archive):
    version = release_tag(tag)[1:]
    # Validate without extracting files or executing anything from the archive.
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as source:
        root = f"mdir-{version}"
        for name in ("go.mod", "go.sum", "LICENSE", "cmd/mdir-fm/main.go"):
            member = source.getmember(f"{root}/{name}")
            if not member.isfile():
                raise ValueError(f"release archive is missing a regular file: {name}")
        with source.extractfile(f"{root}/cmd/mdir-fm/main.go") as main:
            if f'"mdir-fm {version}"'.encode() not in main.read():
                raise ValueError("release tag does not match the CLI version")
    template = Path(__file__).resolve().parents[1] / "packaging/homebrew/mdir.rb.in"
    return template.read_text().replace("@VERSION@", version).replace(
        "@SHA256@", hashlib.sha256(archive).hexdigest()
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag", type=release_tag, help="published tag, for example v0.1.0")
    parser.add_argument("--output", type=Path, help="write atomically to this file instead of stdout")
    args = parser.parse_args()
    url = f"https://github.com/onejae/mdir/archive/refs/tags/{args.tag}.tar.gz"
    try:
        with urlopen(url, timeout=60) as response:
            formula = render_formula(args.tag, response.read())
        if args.output is None:
            sys.stdout.write(formula)
        else:
            temporary = None
            try:
                with tempfile.NamedTemporaryFile(
                    mode="w", encoding="utf-8", dir=args.output.parent,
                    prefix=f".{args.output.name}.", delete=False,
                ) as output:
                    temporary = Path(output.name)
                    output.write(formula)
                temporary.replace(args.output)
            finally:
                if temporary is not None:
                    temporary.unlink(missing_ok=True)
    except (OSError, URLError, tarfile.TarError, KeyError, ValueError) as error:
        parser.exit(1, f"Cannot generate formula for {args.tag}: {error}\n")


if __name__ == "__main__":
    main()
