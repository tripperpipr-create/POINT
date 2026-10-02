"""Native security/recovery parity attestation for one provisioned Point pack."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import subprocess

from point_runtime_baseline import baseline_descriptor
from point_runtime_metrics import require_owned_guest
from point_runtime_provenance import PROFILE, sandbox_source_digest

ROOT = Path(__file__).resolve().parent.parent
TESTS = ("InheritanceAndRecovery", "AllowlistAndIsolation", "RunResetAndDelta", "InterruptedCommandIsNotReplayed", "DetachedProcessAndControlDescriptors", "JoinMatchesBindAndStartsClean", "CollectorPreservesLiveRecentAndForeignVolumes")


def digest(path):
    with Path(path).open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def verify(args):
    bridge = Path(args.bridge).resolve(); helper = Path(args.helper).resolve()
    output = Path(args.output).resolve(); output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists() or output.with_suffix(".log").exists():
        raise ValueError("Use a new attestation path; native history is immutable")
    names = ["TestVolumeIntegration" + name for name in TESTS]
    if args.baseline_config:
        config = json.loads(Path(args.baseline_config).read_text(encoding="utf-8"))
        _, runtime_digest, env = baseline_descriptor(config)
        require_owned_guest("docker-desktop")
        env.update(POINT_SANDBOX_BACKEND="docker", POINT_SANDBOX_IMAGE=config["imageReference"], POINT_SANDBOX_DOCKER_TEST="1")
        names.append("TestDockerSandboxIntegration")
        pattern = "^(TestVolumeIntegration|TestDockerSandboxIntegration)"
    else:
        manifest = Path(args.manifest).resolve()
        runtime_digest = digest(manifest)
        env = dict(os.environ, POINT_SANDBOX_BACKEND="embedded", POINT_EMBEDDED_RUNTIME=str(manifest))
        pattern = "^TestVolumeIntegration"
    env.update(POINT_SANDBOX_WORKSPACE="volume", POINT_VOLUME_INTEGRATION="1", POINT_RUNTIME_BRIDGE=str(bridge),
               POINT_SANDBOXD_BINARY=str(helper), POINT_SANDBOX_WARM_CONTAINER="on", POINT_SANDBOX_DOWNLOAD_CACHE="on")
    log = output.with_suffix(".log")
    started = dt.datetime.now(dt.timezone.utc).isoformat()
    source_digest = sandbox_source_digest()
    with log.open("wb") as stream:
        result = subprocess.run(["go", "test", "./internal/sandbox", "-run", pattern, "-count=1", "-timeout=15m", "-v"], cwd=ROOT, env=env, stdout=stream, stderr=subprocess.STDOUT, creationflags=0x08000000)
    text = log.read_text(encoding="utf-8", errors="replace")
    passed = result.returncode == 0 and all("--- PASS: " + name + " (" in text for name in names) and source_digest == sandbox_source_digest()
    proof = dict(schema=1, runtimeDigest=runtime_digest, profile=PROFILE, passed=passed, sandboxSourceDigest=source_digest,
                 startedAt=started, completedAt=dt.datetime.now(dt.timezone.utc).isoformat(),
                 tests=names, log=log.name, logDigest=digest(log),
                 bridgeDigest=digest(bridge), helperDigest=digest(helper), returncode=result.returncode)
    output.write_text(json.dumps(proof, indent=2), encoding="utf-8")
    print("Native parity " + ("PASS" if passed else "FAIL") + ": " + str(output), flush=True)
    if not passed:
        raise SystemExit(1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--manifest")
    mode.add_argument("--baseline-config")
    for name in ("bridge", "helper", "output"):
        parser.add_argument("--" + name, required=True)
    verify(parser.parse_args())
