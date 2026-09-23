#!/usr/bin/env python3
"""Check built runtime image architecture without starting API/worker/migrations."""

import argparse
import json
from pathlib import Path
import struct
import subprocess
import tempfile


ELF_MACHINES = {"386": 3, "amd64": 62, "arm": 40, "arm64": 183,
                "ppc64le": 21, "s390x": 22, "riscv64": 243}


def docker(*args):
    result = subprocess.run(["docker", *args], capture_output=True, text=True, timeout=60)
    if result.returncode:
        raise RuntimeError(f"docker {args[0]} failed")
    return result.stdout.strip()


def check_image(image):
    metadata = json.loads(docker("image", "inspect", image))[0]
    architecture = metadata["Architecture"]
    expected = ELF_MACHINES.get(architecture)
    if metadata["Os"] != "linux" or expected is None:
        raise RuntimeError(f"{image}: unsupported runtime platform")
    container = docker("create", "--network", "none", "--entrypoint", "/bin/true", image)
    try:
        with tempfile.TemporaryDirectory(prefix="hcai-runtime-check-") as directory:
            executable = Path(directory) / "hcai"
            docker("cp", container + ":/usr/local/bin/hcai", str(executable))
            with executable.open("rb") as source:
                header = source.read(20)
            if len(header) != 20 or header[:4] != b"\x7fELF" or header[5] not in (1, 2):
                raise RuntimeError(f"{image}: runtime executable is not a valid ELF binary")
            machine = struct.unpack("<H" if header[5] == 1 else ">H", header[18:20])[0]
            if machine != expected:
                raise RuntimeError(f"{image}: image architecture {architecture} requires ELF machine {expected}, found {machine}")
    finally:
        docker("rm", container)
    print(f"ok {image}: linux/{architecture}, ELF machine {machine}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("images", nargs="+", help="Built API, worker or migration image tags")
    args = parser.parse_args()
    for image in args.images:
        check_image(image)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.TimeoutExpired, ValueError, KeyError, OSError) as error:
        raise SystemExit(str(error)) from None
