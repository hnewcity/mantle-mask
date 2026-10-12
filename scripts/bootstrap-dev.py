#!/usr/bin/env python3
"""Install verified, isolated Go/Node toolchains for WSL/Linux development."""
import hashlib
import json
import os
from pathlib import Path
import platform
import tarfile
import urllib.request

root = Path.home() / ".local" / "share" / "mantle-mask-tools"
root.mkdir(parents=True, exist_ok=True)
arch = {"x86_64": "amd64", "aarch64": "arm64"}[platform.machine()]


def read(url):
    with urllib.request.urlopen(url, timeout=90) as response:
        return response.read()


def install(url, checksum, dest):
    if dest.exists():
        return
    filename = url.rsplit("/", 1)[-1]
    archive = root / filename
    print(f"Downloading {filename}", flush=True)
    data = read(url)
    if hashlib.sha256(data).hexdigest() != checksum:
        raise RuntimeError(f"Checksum mismatch: {filename}")
    archive.write_bytes(data)
    with tarfile.open(archive) as tar:
        # Official archives only, with integrity verification above.
        tar.extractall(root, filter="data")
    archive.unlink()


go = next(v for v in json.loads(read("https://go.dev/dl/?mode=json")) if v["stable"])
go_file = next(f for f in go["files"] if f["os"] == "linux" and f["arch"] == arch and f["kind"] == "archive")
install("https://go.dev/dl/" + go_file["filename"], go_file["sha256"], root / "go")
node = next(v for v in json.loads(read("https://nodejs.org/dist/index.json")) if v["version"].startswith("v24.") and v["lts"])
node_arch = "x64" if arch == "amd64" else "arm64"
node_name = f"node-{node['version']}-linux-{node_arch}"
sums = read(f"https://nodejs.org/dist/{node['version']}/SHASUMS256.txt").decode()
sha = next(line.split()[0] for line in sums.splitlines() if line.endswith(node_name + ".tar.xz"))
install(f"https://nodejs.org/dist/{node['version']}/{node_name}.tar.xz", sha, root / node_name)
node_link = root / "node"
if not node_link.exists():
    node_link.symlink_to(root / node_name, target_is_directory=True)
print(f'export PATH="{root}/go/bin:{root}/node/bin:$PATH"', flush=True)
