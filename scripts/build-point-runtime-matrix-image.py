"""Build one pinned Node/Go/PHP image and package identical OCI bytes for both engines.

Uses only an already provisioned Point-owned Moby guest. Docker Desktop and
Windows mounts are unnecessary. Consumer packs use a new manifest identity.
"""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("point_pack", ROOT / "scripts/build-point-runtime-wsl-pack.py")
pack = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pack)


def oci_identity(file):
    with tarfile.open(file) as archive:
        index = json.load(archive.extractfile("index.json"))
        entries = [entry for entry in index["manifests"] if entry["mediaType"].endswith("image.manifest.v1+json")]
        if len(entries) != 1:
            raise ValueError("Expected one canonical OCI image manifest")
        digest = entries[0]["digest"]
        raw = archive.extractfile("blobs/sha256/" + digest.removeprefix("sha256:")).read()
        if "sha256:" + hashlib.sha256(raw).hexdigest() != digest:
            raise ValueError("OCI manifest digest differs")
        manifest = json.loads(raw)
        for blob in [manifest["config"], *manifest["layers"]]:
            stream = archive.extractfile("blobs/sha256/" + blob["digest"].removeprefix("sha256:"))
            if "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest() != blob["digest"]:
                raise ValueError("OCI config/layer content differs")
        return digest, manifest["config"]["digest"]


def build(args):
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    paths = {engine: Path(getattr(args, engine + "_pack")).resolve() for engine in ("moby", "podman")}
    manifests = {engine: json.loads(path.read_text(encoding="utf-8")) for engine, path in paths.items()}
    for engine, manifest in manifests.items():
        if manifest["engine"] != engine:
            raise ValueError("Runtime engine does not match the supplied pack")
        if pack.sha256(paths[engine].parent / manifest["image"]["file"]) != manifest["image"]["sha256"]:
            raise ValueError("Runtime sandbox archive changed")
    base_content, base_config = oci_identity(paths["moby"].parent / manifests["moby"]["image"]["file"])
    other_content, other_config = oci_identity(paths["podman"].parent / manifests["podman"]["image"]["file"])
    if (base_content, base_config) != (other_content, other_config):
        raise ValueError("Candidate sandbox images have different OCI contents")
    digest = pack.sha256(paths["moby"])
    distribution = "point-runtime-moby-" + digest.removeprefix("sha256:")[:24]
    owner = Path(os.environ["LOCALAPPDATA"]) / "Point/runtime" / digest.removeprefix("sha256:") / "owner.json"
    identity = json.loads(owner.read_text(encoding="utf-8"))
    if identity["digest"] != digest or identity["distribution"] != distribution or identity["state"] != "ready":
        raise ValueError("Point-owned provisioned builder guest required")
    dockerfile = (ROOT / "Dockerfile.sandbox-php").read_text(encoding="utf-8")
    original = "FROM point-agent-sandbox:1.2.2"
    if dockerfile.count(original) != 1:
        raise ValueError("Review changed PHP image ancestry before rebuilding")
    dockerfile = dockerfile.replace(original, "FROM " + manifests["moby"]["imageDigest"])
    context = output / "context.tar"
    with tarfile.open(context, "w", format=tarfile.GNU_FORMAT) as archive:
        raw = dockerfile.encode(); entry = tarfile.TarInfo("Dockerfile"); entry.size = len(raw); entry.mode = 0o600
        archive.addfile(entry, io.BytesIO(raw))
    image = "point-runtime-matrix:20261002"
    with context.open("rb") as archive:
        pack.guest(distribution, ["/usr/bin/docker", "build", "-t", image, "-"], stdin=archive)
    moby_id = pack.guest(distribution, ["/usr/bin/docker", "image", "inspect", image, "--format", "{{.Id}}"], stdout=subprocess.PIPE).stdout.decode().strip()
    exported = output / "exported.tar"
    with exported.open("wb") as archive:
        pack.guest(distribution, ["/usr/bin/docker", "save", image], stdout=archive)
    canonical = output / "sandbox-image.tar"
    with tarfile.open(exported, "r|*") as src, tarfile.open(canonical, "w|", format=tarfile.GNU_FORMAT) as dst:
        for entry in src:
            if entry.name.lstrip("./") != "manifest.json":
                dst.addfile(entry, src.extractfile(entry) if entry.isfile() else None)
    content, config = oci_identity(canonical)
    for engine in ("moby", "podman"):
        path = output / engine; path.mkdir()
        rootfs = paths[engine].parent / manifests[engine]["rootfs"]["file"]
        if pack.sha256(rootfs) != manifests[engine]["rootfs"]["sha256"]:
            raise ValueError("Runtime Linux archive changed")
        for source, target in ((rootfs, path / "rootfs.tar"), (canonical, path / "sandbox-image.tar")):
            try: os.link(source, target)
            except OSError: shutil.copyfile(source, target)
        manifest = dict(manifests[engine])
        manifest.update(imageReference=image, imageDigest=moby_id if engine == "moby" else config,
                        rootfs=dict(file="rootfs.tar", sha256=pack.sha256(rootfs)),
                        image=dict(file="sandbox-image.tar", sha256=pack.sha256(canonical)),
                        provenance=dict(baseRuntimeDigest=pack.sha256(paths[engine]), imageContentDigest=content,
                                        dockerfileSHA256="sha256:" + hashlib.sha256(dockerfile.encode()).hexdigest()))
        (path / "runtime.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    (output / "identity.json").write_text(json.dumps(dict(image=image, imageContentDigest=content, mobyExecutionId=moby_id, podmanExecutionId=config), indent=2), encoding="utf-8")
    print("Shared OCI matrix image: " + content)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--moby-pack", required=True)
    parser.add_argument("--podman-pack", required=True)
    parser.add_argument("--output", required=True)
    build(parser.parse_args())
