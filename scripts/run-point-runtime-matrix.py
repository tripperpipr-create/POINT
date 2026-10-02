"""Run the fixed full-quest matrix on isolated projects/databases on this PC.

Credentials must be supplied as environment variables; never appear in config,
arguments or evidence. Unavailable candidates/baseline are recorded explicitly.
At least three repetitions per project and regime are required for qualification.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import msvcrt
import urllib.request

from point_runtime_metrics import Collector, Job, atomic_json, counter_delta, guest_counters, require_owned_guest, running_distributions, summarize_frames
from point_runtime_baseline import baseline_descriptor
from point_runtime_provenance import PROFILE, sandbox_source_digest

ROOT = Path(__file__).resolve().parent.parent
PROJECTS = ("node-typescript", "go", "php")


class NoModelRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Credentials are authorized for the pinned endpoint only.


def wait_file(path, child, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return json.loads(path.read_text(encoding="utf-8"))
        if child.poll() is not None:
            raise RuntimeError("Trial exited before " + path.name)
        time.sleep(.2)
    raise TimeoutError("Trial did not produce " + path.name)


def sha(file):
    with Path(file).open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def quest_source_digest():
    files = [ROOT / "go.mod", ROOT / "go.sum"]
    for directory in ("internal", "cmd"):
        files.extend((ROOT / directory).rglob("*.go"))
    material = {str(file.relative_to(ROOT)).replace("\\", "/"): sha(file) for file in sorted(files)}
    return "sha256:" + hashlib.sha256(json.dumps(material, sort_keys=True).encode()).hexdigest()


def pack_owner(manifest_path):
    path = Path(manifest_path).resolve()
    manifest = json.loads(path.read_text(encoding="utf-8"))
    digest = sha(path)
    engine = manifest["engine"]
    if engine not in ("moby", "podman"):
        raise ValueError("Unsupported runtime candidate")
    distribution = "point-runtime-" + engine + "-" + digest.removeprefix("sha256:")[:24]
    base = Path(os.environ["LOCALAPPDATA"]) / "Point/runtime" / digest.removeprefix("sha256:")
    owner = json.loads((base / "owner.json").read_text(encoding="utf-8"))
    if owner["digest"] != digest or owner["distribution"] != distribution or owner["state"] != "ready":
        raise ValueError("Candidate lacks matching Point ownership")
    return manifest, digest, distribution, base


def stop_owned(manifest_path):
    _, _, distribution, base = pack_owner(manifest_path)
    deadline = time.monotonic() + 30
    while True:
        handles = []
        try:
            # Never wait on a lease while holding control: its releasing owner
            # needs that same control lock to journal its last activity.
            control = (base / "control.lock").open("r+b"); handles.append(control)
            msvcrt.locking(control.fileno(), msvcrt.LK_NBLCK, 1)
            for file in sorted((base / "leases").glob("*.lock")):
                try:
                    handle = file.open("r+b")
                except FileNotFoundError:
                    continue
                handles.append(handle)
                msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
            subprocess.run(["wsl.exe", "--terminate", distribution], check=True, capture_output=True, creationflags=0x08000000, timeout=30)
            return
        except PermissionError:
            if time.monotonic() >= deadline:
                raise RuntimeError("Owned runtime remains busy; no active execution was stopped") from None
        finally:
            for handle in reversed(handles):
                handle.close()
        time.sleep(.1)


def tree_bytes(paths):
    seen = set(); total = 0
    for root in paths:
        root = Path(root).resolve()
        if not root.exists():
            raise ValueError("Measured storage root missing: " + str(root))
        entries = [root] if root.is_file() else root.rglob("*")
        for file in entries:
            if file.is_symlink():
                raise ValueError("Storage measurement encountered a symlink")
            if not file.is_file():
                continue
            stat = file.stat(); identity = (stat.st_dev, stat.st_ino)
            if identity not in seen:
                seen.add(identity); total += stat.st_size
    return total


def preflight(config, bridge, helper, output):
    if not re.fullmatch(r"[a-z0-9-]{1,64}", config.get("id", "")):
        raise ValueError("Invalid configuration identifier")
    if config["engine"] == "docker":
        identity, digest, _ = baseline_descriptor(config)
        atomic_json(output / (config["id"] + "-readiness.json"), identity)
        manifest = dict(imageReference=config["imageReference"], imageDigest=config["imageDigest"])
        distribution, base = "docker-desktop", None
    else:
        manifest, digest, distribution, base = pack_owner(config["manifest"])
        result = subprocess.run([str(bridge), "status", "-manifest", str(Path(config["manifest"]).resolve())], capture_output=True, creationflags=0x08000000, timeout=90)
        (output / (config["id"] + "-readiness.json")).write_bytes(result.stdout)
        if result.returncode:
            raise RuntimeError("Native security readiness failed: " + result.stderr.decode(errors="replace")[-2000:])
    attestation = Path(config["securityEvidence"]).resolve()
    proof = json.loads(attestation.read_text(encoding="utf-8"))
    if proof.get("runtimeDigest") != digest or proof.get("profile") != PROFILE or proof.get("passed") is not True or proof.get("sandboxSourceDigest") != sandbox_source_digest():
        raise ValueError("Full native runtime security parity attestation missing or differs")
    if sha(attestation.parent / proof["log"]) != proof["logDigest"] or proof["bridgeDigest"] != sha(bridge) or proof["helperDigest"] != sha(helper):
        raise ValueError("Native security parity evidence/bridge/helper changed")
    return manifest, distribution, base


def model_preflight(env):
    route = env.get("POINT_LLMUX_BASE_URL", "").rstrip("/")
    if route != "https://llmux.centrofinans.ru/v1":
        raise ValueError("Original approved model route required; no fallback")
    request = urllib.request.Request(route + "/models", headers={"Authorization": "Bearer " + env["POINT_LLMUX_API_KEY"]})
    try:
        with urllib.request.build_opener(NoModelRedirect()).open(request, timeout=15) as response:
            models = json.loads(response.read(4 * 1024 * 1024)).get("data", [])
    except Exception as error:
        # Do not retain HTTP response bodies/headers or credentials.
        raise RuntimeError("Pinned model route unavailable (" + type(error).__name__ + ")") from None
    if not any(model.get("id") == "Qwen3.8-27B" for model in models):
        raise ValueError("Pinned Qwen3.8-27B is absent from the real provider catalog; no fallback")


def run_trial(binary, config, project, regime, trial, env, directory):
    directory.mkdir()
    docker = config["engine"] == "docker"
    if docker:
        _, digest, docker_env = baseline_descriptor(config)
        env = dict(env, **{k: v for k, v in docker_env.items() if k.startswith("DOCKER_")})
        for key in ("DOCKER_HOST", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"):
            env.pop(key, None)
        manifest = dict(imageReference=config["imageReference"], imageDigest=config["imageDigest"])
        distribution, base = "docker-desktop", None
    else:
        manifest, digest, distribution, base = pack_owner(config["manifest"])
    # Cold means a stopped owned guest, fresh database/project, dependencies and
    # application state. Host disk page-cache flushing would affect other apps
    # and is deliberately excluded from this declared regime.
    if regime == "cold" and not docker:
        stop_owned(config["manifest"])
    elif regime == "warm" and distribution not in running_distributions():
        raise ValueError("Warm trial requires the preceding cold trial's running guest")
    require_owned_guest(distribution)
    trial_env = dict(env, POINT_RUNTIME_MATRIX="1", POINT_BENCH_TRIAL_DIR=str(directory),
                     POINT_BENCH_CONFIGURATION=config["id"], POINT_BENCH_PROJECT=project,
                     POINT_BENCH_REGIME=regime, POINT_SANDBOX_BACKEND="docker" if docker else "embedded",
                     POINT_SANDBOX_WORKSPACE="bind" if docker else "volume", POINT_EMBEDDED_RUNTIME="" if docker else str(Path(config["manifest"]).resolve()),
                     POINT_VERIFY_SERVICE=config.get("verify", "shadow"), POINT_BENCH_SECURITY_ATTESTED="1",
                     POINT_SANDBOX_WARM_CONTAINER=config.get("warmContainers", "on"),
                     POINT_SANDBOX_DOWNLOAD_CACHE=config.get("dependencyCache", "on"),
                     POINT_BENCH_IMAGE=manifest["imageReference"], POINT_BENCH_EXECUTION_IMAGE_DIGEST=manifest["imageDigest"],
                     POINT_BENCH_IMAGE_CONTENT_DIGEST=config["imageContentDigest"], POINT_BENCH_RUNTIME_DIGEST=digest)
    # Match the limits recorded in the approved fixture; do not inherit an
    # operator's unrelated environment overrides into a comparison silently.
    trial_env.update(POINT_SANDBOX_MEMORY="2g", POINT_SANDBOX_CPUS="2", POINT_SANDBOX_PIDS="256", POINT_SANDBOX_USER="10001:10001")
    for key in ("GOFLAGS", "GOMAXPROCS", "CGO_ENABLED", "NODE_ENV", "LANG", "LC_ALL", "TZ", "TERM", "CI", "NO_COLOR", "FORCE_COLOR", "PYTHONUTF8", "PYTHONDONTWRITEBYTECODE", "RUST_BACKTRACE"):
        trial_env.pop(key, None)
    trial_env.update(CI="1", LANG="C.UTF-8", TZ="UTC", NO_COLOR="1")
    log = (directory / "quest.log").open("wb")
    test = "TestRuntimeMatrixReplay" if config.get("kind") == "operation_replay" else "TestRuntimeMatrixLiveQuest"
    child = subprocess.Popen([str(binary), "-test.run=^" + test + "$", "-test.v", "-test.timeout=45m"], cwd=ROOT, env=trial_env, stdout=log, stderr=subprocess.STDOUT, creationflags=0x08000000)
    job = None; collector = None
    try:
        wait_file(directory / "started.json", child, 60)
        job = Job(child.pid)
        (directory / "start.gate").touch()
        prepared = wait_file(directory / "prepared.json", child, 180)
        require_owned_guest(distribution)
        collector = Collector(job, directory, distribution)
        first = collector.frame(); before_job = job.accounting(); before_guest = guest_counters(distribution)
        started = dt.datetime.now(dt.timezone.utc).isoformat()
        (directory / "approve.gate").touch()
        receipt = wait_file(directory / "receipt.json", child, 2100)
        after_guest = guest_counters(distribution); after_job = job.accounting(); last = collector.frame()
        ended = dt.datetime.now(dt.timezone.utc).isoformat()
        collector.stop(); frames = collector.frames[first:last + 1]; collector = None
        raw = dict(schema=1, coverage="whole_environment", startedAt=started, endedAt=ended,
                   jobBefore=before_job, jobAfter=after_job, guestBefore=before_guest, guestAfter=after_guest,
                   frames=frames, definition="job CPU/IO + protected WSL/infra CPU; working-set sum; guest block/NIC counters; model encrypted TCP bytes",
                   sampleIntervalMs=750, taskBounds=receipt, prepared=prepared)
        atomic_json(directory / "resources.json", raw)
        resources = summarize_frames(frames, before_job, after_job)
        guest = counter_delta(before_guest, after_guest)
        resources["diskReadBytes"] += guest["diskReadBytes"]
        resources["diskWriteBytes"] += guest["diskWriteBytes"]
        resources["networkBytes"] = guest["networkBytes"] + receipt["modelNetworkBytes"]
        storage = [Path(p) for p in config["storagePaths"]] if docker else [base / "disk", Path(config["manifest"]).resolve().parent]
        resources["storageBytes"] = tree_bytes([*storage, directory])
        (directory / "inspect.gate").touch()
        result = wait_file(directory / "sample.json", child, 1000)
        code = child.wait(timeout=180)
        result.update(resources, resourceCoverage="whole_environment", resourceTrace="resources.json", trial=trial,
                      regimeDefinition="fresh project/database/dependencies; provisioned engine ready before approval" if regime == "cold" else "running guest + fresh project/database; per-quest verified caches",
                      guestRestartedBeforePreparation=regime == "cold" and not docker)
        if code:
            result["trialFailure"] = "Quest quality gate failed"
        atomic_json(directory / "sample.json", result)
        return result
    finally:
        # Never repeat a command/quest with unknown outcome. A failed trial stays
        # in the ledger; cancellation kills only this explicitly owned child.
        if collector:
            atomic_json(directory / "resources-incomplete.json", dict(coverage="incomplete", frames=collector.frames, errors=collector.errors))
            try:
                collector.stop()
            except Exception:
                pass
        if child.poll() is None:
            child.terminate(); child.wait(timeout=30)
        if job:
            job.close()
        log.close()


def main(args):
    if os.name != "nt":
        raise ValueError("Native runtime matrix currently targets Windows 11 x64")
    config = json.loads(Path(args.config).read_text(encoding="utf-8"))
    output = Path(args.output).resolve(); output.mkdir(parents=True, exist_ok=False)
    bridge = Path(args.bridge).resolve()
    if bridge.name != "point-runtime.exe" or not bridge.is_file():
        raise ValueError("Trusted absolute runtime bridge required")
    env = dict(os.environ, POINT_RUNTIME_BRIDGE=str(bridge), POINT_SANDBOXD_BINARY=str(Path(args.helper).resolve()))
    needs_model = any(c.get("kind") != "operation_replay" for c in config["configurations"])
    if needs_model and (not env.get("POINT_LLMUX_API_KEY") or not env.get("POINT_LLMUX_BASE_URL")):
        raise ValueError("Supply model credentials/route through environment")
    binary = output / "runtime-matrix.test.exe"
    source_digest = quest_source_digest()
    subprocess.run(["go", "test", "-c", "-o", str(binary), "./internal/acceptance"], cwd=ROOT, check=True, env=env, creationflags=0x08000000)
    if quest_source_digest() != source_digest:
        raise ValueError("Quest sources changed while compiling; no trial started")
    # Store identities, not the caller's environment or provider credential.
    atomic_json(output / "coordinator.json", dict(schema=1, questBinaryDigest=sha(binary), questSourceDigest=source_digest,
                bridgeDigest=sha(bridge), helperDigest=sha(Path(args.helper)),
                configurationDigest="sha256:" + hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest(),
                tooling={name: sha(ROOT / "scripts" / name) for name in
                         ("run-point-runtime-matrix.py", "point_runtime_metrics.py", "point-runtime-metrics.ps1",
                          "point_runtime_baseline.py", "point_runtime_provenance.py")},
                securityProfile=PROFILE, trials=args.trials,
                limits=dict(memory="2g", cpu="2", pids=256, user="10001:10001")))
    samples = []; replays = []; unavailable = []; candidates = []
    atomic_json(output / "raw-samples.json", dict(schema=1, baseline=config["baseline"], samples=samples, replays=replays, failures=unavailable, repetitions=args.trials))
    for candidate in config["configurations"]:
        try:
            preflight(candidate, bridge, Path(args.helper).resolve(), output)
            candidates.append(candidate)
        except Exception as error:
            unavailable.append(dict(configuration=candidate["id"], reason=str(error)))
    if needs_model:
        try:
            model_preflight(env)
        except Exception as error:
            unavailable.append(dict(phase="model_preflight", reason=str(error)))
            candidates = [c for c in candidates if c.get("kind") == "operation_replay"]
    atomic_json(output / "preflight.json", dict(unavailable=unavailable, available=[c["id"] for c in candidates]))
    if not candidates:
        atomic_json(output / "raw-samples.json", dict(schema=1, baseline=config["baseline"], samples=[], replays=[], failures=unavailable, repetitions=args.trials))
        print("No runnable trials; preflight evidence retained: " + str(output), flush=True)
        return
    for manifest in config.get("idleRuntimeManifests", []):
        stop_owned(manifest)
    for file in config.get("completedBuilderOwners", []):
        owner = json.loads(Path(file).read_text(encoding="utf-8"))
        name = owner.get("distribution", "")
        if owner.get("state") != "pack_ready" or not re.fullmatch(r"point-build-(moby|podman)-[a-f0-9]{20}", name):
            raise ValueError("Only a recorded completed Point preparation guest can stop")
        subprocess.run(["wsl.exe", "--terminate", name], check=True, capture_output=True, creationflags=0x08000000, timeout=30)
    iterations = 0
    for trial in range(1, args.trials + 1):
        for project in PROJECTS:
            order = candidates[(trial + PROJECTS.index(project)) % len(candidates):] + candidates[:(trial + PROJECTS.index(project)) % len(candidates)] if candidates else []
            # Keep each cold/warm pair on the same guest. Switching engines
            # first terminates inactive owned guests and would invalidate warm.
            for candidate in order:
                for regime in ("cold", "warm"):
                    if args.max_tasks and iterations >= args.max_tasks:
                        break
                    name = f'{candidate["id"]}-{project}-{regime}-{trial}'
                    print(("Operation replay: " if candidate.get("kind") == "operation_replay" else "Full quest: ") + name, flush=True)
                    # Stop only owned, inactive candidate guests before measuring
                    # the shared VM; refuse live leases and foreign distributions.
                    directory = output / name
                    try:
                        for other in config["configurations"]:
                            if other["engine"] != "docker" and other.get("manifest") and Path(other["manifest"]).resolve() != Path(candidate.get("manifest", "")).resolve():
                                stop_owned(other["manifest"])
                        sample = run_trial(binary, candidate, project, regime, trial, env, directory)
                        sample["evidence"] = name + "/evidence.json"
                        (replays if sample.get("kind") == "operation_replay" else samples).append(sample)
                    except Exception as error:
                        directory.mkdir(exist_ok=True)
                        atomic_json(directory / "failure.json", dict(reason=str(error), configuration=candidate["id"], project=project, regime=regime, trial=trial, unknownOutcome="approved.json exists but receipt.json missing"))
                        unavailable.append(dict(trial=name, reason=str(error)))
                    iterations += 1
                    atomic_json(output / "raw-samples.json", dict(schema=1, baseline=config["baseline"], samples=samples, replays=replays, failures=unavailable, repetitions=args.trials))
    print("Raw evidence retained: " + str(output), flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--bridge", required=True)
    parser.add_argument("--helper", required=True)
    parser.add_argument("--trials", type=int, default=3)
    parser.add_argument("--max-tasks", type=int, default=0, help="diagnostic pilot only; never qualification")
    main(parser.parse_args())
