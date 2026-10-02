"""Read-only attribution for an explicitly configured local Docker baseline.

No Docker context/configuration is changed. A baseline requires an idle local
Linux engine and explicit Desktop VHD paths; foreign workloads invalidate it.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
from point_runtime_provenance import PROFILE


def baseline_environment(config):
    context = config.get("dockerContext")
    if context not in ("desktop-linux", "default"):
        raise ValueError("Explicit local Docker context required")
    if config.get("resourceGuest") != "docker-desktop":
        raise ValueError("Docker Desktop resource guest must be explicit")
    roots = config.get("storagePaths", [])
    allowed = (Path(os.environ["LOCALAPPDATA"]) / "Docker" / "wsl").resolve()
    if not roots:
        raise ValueError("Explicit Docker Desktop VHD storage paths required")
    for raw in roots:
        path = Path(raw)
        if not path.is_absolute() or path.is_symlink() or not path.resolve().is_relative_to(allowed):
            raise ValueError("Baseline storage must stay inside Docker's WSL directory")
        if path.suffix.lower() != ".vhdx" or not path.is_file():
            raise ValueError("Baseline storage must name existing VHDX files")
    env = dict(os.environ, DOCKER_CONTEXT=context)
    env.pop("DOCKER_HOST", None)
    env.pop("DOCKER_TLS_VERIFY", None)
    env.pop("DOCKER_CERT_PATH", None)
    return env


def docker_read(args, env):
    result = subprocess.run(["docker", *args], env=env, capture_output=True,
                            creationflags=0x08000000, timeout=45)
    if result.returncode:
        raise RuntimeError("Docker baseline daemon/operation unavailable: " + args[0])
    return result.stdout.decode("utf-8").strip()


def baseline_descriptor(config):
    env = baseline_environment(config)
    endpoint = docker_read(["context", "inspect", config["dockerContext"], "--format", "{{.Endpoints.docker.Host}}"], env)
    if endpoint not in ("npipe:////./pipe/dockerDesktopLinuxEngine", "npipe:////./pipe/docker_engine"):
        raise ValueError("Baseline refuses remote engines or unexpected local endpoints")
    info = json.loads(docker_read(["info", "--format", "{{json .}}"], env))
    if info.get("ServerErrors") or not info.get("ServerVersion"):
        raise RuntimeError("Docker baseline daemon unavailable")
    if info.get("OSType") != "linux":
        raise ValueError("Linux Docker engine required")
    if docker_read(["ps", "--quiet"], env):
        raise ValueError("Baseline refuses attribution with existing running Docker containers")
    image = config.get("imageReference", "")
    execution = config.get("imageDigest", "")
    if not image or not execution.startswith("sha256:"):
        raise ValueError("Pinned baseline image/execution identity required")
    observed = docker_read(["image", "inspect", image, "--format", "{{.Id}}"], env)
    if observed != execution or execution != config.get("imageContentDigest"):
        raise ValueError("Baseline image differs from the shared OCI manifest digest")
    identity = dict(engine="docker", engineVersion=info["ServerVersion"],
                    kernel=info.get("KernelVersion"), os=info.get("OperatingSystem"),
                    storageDriver=info.get("Driver"), cgroupVersion=info.get("CgroupVersion"),
                    imageDigest=observed, profile=PROFILE)
    digest = "sha256:" + hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
    return identity, digest, env
