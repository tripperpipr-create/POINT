"""Prepare a Point-owned runtime pack without Docker Desktop.

The operator supplies a verified Ubuntu WSL archive and its SHA-256. No existing
distro or Docker context is used. Files enter the guest through stdin archives;
Windows automount and interop are disabled before its first boot.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import threading
import uuid

ROOT = Path(__file__).resolve().parent.parent
CONF = b"[boot]\nsystemd=true\n[automount]\nenabled=false\n[interop]\nenabled=false\nappendWindowsPath=false\n"


def sha256(file):
    with Path(file).open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def prepare_rootfs(source, target, expected):
    if sha256(source) != expected:
        raise ValueError("Base WSL archive checksum differs; import refused")
    # Stream entries, never extract Linux links/permissions into Windows.
    with tarfile.open(source, "r|*") as src, tarfile.open(target, "w|") as dst:
        for entry in src:
            if entry.name.strip("./") == "etc/wsl.conf":
                continue
            dst.addfile(entry, src.extractfile(entry) if entry.isfile() else None)
        entry = tarfile.TarInfo("etc/wsl.conf")
        entry.mode, entry.size = 0o644, len(CONF)
        dst.addfile(entry, io.BytesIO(CONF))


def host(args, **kw):
    capture = "stdout" not in kw
    if capture:
        kw["stdout"] = subprocess.PIPE
    kw.setdefault("stderr", subprocess.PIPE)
    archive = kw.pop("stdin", None)
    if archive is not None:
        # wsl.exe must receive a pipe, not an inherited Windows file handle.
        process = subprocess.Popen(args, stdin=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW, **kw)
        pipe = process.stdin

        def pump():
            try:
                for chunk in iter(lambda: archive.read(1024 * 1024), b""):
                    pipe.write(chunk)
            except BrokenPipeError:
                pass
            finally:
                pipe.close()

        writer = threading.Thread(target=pump)
        writer.start()
        process.stdin = None
        out, err = process.communicate()
        writer.join()
        result = subprocess.CompletedProcess(args, process.returncode, out, err)
    else:
        result = subprocess.run(args, creationflags=subprocess.CREATE_NO_WINDOW, **kw)
    if result.returncode:
        detail = (result.stderr or b"") + (result.stdout or b"" if capture else b"")
        print(detail.decode("utf-8", errors="replace")[-6000:], flush=True)
        result.check_returncode()
    return result


def guest(name, args, **kw):
    return host(["wsl.exe", "--distribution", name, "--user", "root", "--exec",
                 "/usr/bin/env", "-i", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                 "HOME=/root", "DEBIAN_FRONTEND=noninteractive", *args], **kw)


def build(args):
    if os.name != "nt":
        raise ValueError("WSL pack preparation requires Windows")
    expected = "sha256:" + args.base_sha256.removeprefix("sha256:").lower()
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", expected):
        raise ValueError("Base archive must have a pinned SHA-256")
    if args.package_version and not re.fullmatch(r"[a-zA-Z0-9.+:~_-]+", args.package_version):
        raise ValueError("Invalid exact package version")
    output = Path(args.output).resolve()
    if args.resume:
        state = json.loads((output / "builder-owner.json").read_text(encoding="utf-8"))
        name = state["distribution"]
        if (state.get("engine") != args.engine or state.get("baseSHA256") != expected or
                not re.fullmatch("point-build-" + args.engine + "-[a-f0-9]{20}", name) or
                state.get("state") != "rootfs_exported" or (output / "runtime.json").exists()):
            raise ValueError("Only a matching Point builder after rootfs export can resume")
        if args.package_version and args.package_version != state.get("packageVersion"):
            raise ValueError("Resume cannot change pinned package version")
    else:
        output.mkdir(parents=True, exist_ok=False)
        name = "point-build-" + args.engine + "-" + uuid.uuid4().hex[:20]
        state = {"schema": 1, "distribution": name, "engine": args.engine,
                 "baseSHA256": expected, "baseSource": args.base_source, "state": "preparing"}
    state_file = output / "builder-owner.json"

    def save():
        state_file.write_text(json.dumps(state, indent=2), encoding="utf-8")

    package = "docker.io" if args.engine == "moby" else "podman"
    program = "/usr/bin/docker" if args.engine == "moby" else "/usr/bin/podman"
    if not args.resume:
        save()
        prepared = output / "base-owned.tar"
        prepare_rootfs(args.base_rootfs, prepared, expected)
        host(["wsl.exe", "--import", name, str(output / "builder-disk"), str(prepared), "--version", "2"])
        state["state"] = "imported"
        save()
        guest(name, ["apt-get", "update"])
        version = args.package_version
        state["packageVersion"] = version
        save()
        guest(name, ["apt-get", "install", "-y", "--no-install-recommends", "ca-certificates",
                     "iptables", "python3", "systemd", "systemd-sysv", package + "=" + version])
        if args.engine == "moby":
            guest(name, ["systemctl", "enable", "--now", "docker"])
        engine_version = guest(name, [program, "--version"], stdout=subprocess.PIPE).stdout.decode()
        match = re.search(r"\d+\.\d+\.\d+", engine_version)
        if not match:
            raise ValueError("Engine version cannot be pinned")
        state["engineVersion"] = match[0]
        save()
        with (output / "packages.txt").open("wb") as inventory:
            guest(name, ["dpkg-query", "-W"], stdout=inventory)
        host(["wsl.exe", "--terminate", name])
        host(["wsl.exe", "--export", name, str(output / "rootfs.tar")])
        state["state"] = "rootfs_exported"
        save()
    version = state["packageVersion"]
    image = "point-agent-sandbox:1.2.2"
    if args.image_archive:
        if not args.image_sha256 or sha256(args.image_archive) != "sha256:" + args.image_sha256.removeprefix("sha256:"):
            raise ValueError("Input sandbox archive must have a matching pinned SHA-256")
        # Docker 29 saves OCI plus a Docker compatibility manifest. Keep OCI
        # config/layers intact. Docker's execution ID names the manifest while
        # Podman's execution ID names the config; the canonical content matches.
        canonical = output / "input-image-oci.tar"
        with tarfile.open(args.image_archive, "r|*") as src, tarfile.open(canonical, "w|", format=tarfile.GNU_FORMAT) as dst:
            for entry in src:
                if entry.name.lstrip("./") == "manifest.json":
                    continue
                dst.addfile(entry, src.extractfile(entry) if entry.isfile() else None)
        with canonical.open("rb") as archive:
            guest(name, [program, "load"], stdin=archive)
        image = args.image_reference
    else:
        # Minimal trusted build context. Do not send the repository or .env files.
        with (output / "sandbox-build-context.tar").open("w+b") as context:
            with tarfile.open(fileobj=context, mode="w", format=tarfile.GNU_FORMAT) as tar:
                for file in [ROOT / "Dockerfile.sandbox", ROOT / "go.mod", ROOT / "go.sum",
                             *(ROOT / "internal/egress").rglob("*.go"),
                             *(ROOT / "cmd/point-egress-gateway").rglob("*.go")]:
                    archive_name = "Dockerfile" if file.name == "Dockerfile.sandbox" else str(file.relative_to(ROOT)).replace("\\", "/")
                    tar.add(file, arcname=archive_name, recursive=False)
            context.seek(0)
            guest(name, [program, "build", "-t", image, "-"], stdin=context)
    digest = guest(name, [program, "image", "inspect", image, "--format", "{{.Id}}"], stdout=subprocess.PIPE).stdout.decode().strip()
    if args.engine == "podman" and re.fullmatch(r"[a-f0-9]{64}", digest):
        digest = "sha256:" + digest
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest):
        raise ValueError("Invalid sandbox image digest")
    if args.image_archive:
        shutil.copyfile(canonical, output / "sandbox-image.tar")
    else:
        with (output / "sandbox-image.tar").open("wb") as archive:
            guest(name, [program, "save", image], stdout=archive)
    manifest = {"schema": 1, "engine": args.engine, "engineVersion": state["engineVersion"],
                "license": "Apache-2.0", "imageReference": image, "imageDigest": digest,
                "rootfs": {"file": "rootfs.tar", "sha256": sha256(output / "rootfs.tar")},
                "image": {"file": "sandbox-image.tar", "sha256": sha256(output / "sandbox-image.tar")},
                "provenance": {"baseSHA256": expected, "baseSource": args.base_source,
                               "package": package, "packageVersion": version}}
    (output / "runtime.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    state["state"] = "pack_ready"
    save()
    host(["wsl.exe", "--terminate", name])
    print("Pinned runtime pack: " + str(output / "runtime.json"), flush=True)
    print("Only the recorded Point builder was stopped; its disk is retained.", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--engine", choices=["moby", "podman"], required=True)
    parser.add_argument("--base-rootfs", required=True)
    parser.add_argument("--base-sha256", required=True)
    parser.add_argument("--base-source", required=True)
    parser.add_argument("--package-version", required=True)
    parser.add_argument("--image-archive")
    parser.add_argument("--image-sha256")
    parser.add_argument("--image-reference", default="point-agent-sandbox:1.2.2")
    parser.add_argument("--output", required=True)
    parser.add_argument("--resume", action="store_true")
    build(parser.parse_args())
