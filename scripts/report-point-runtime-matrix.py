"""Summarize retained observations without qualifying replay as model quests."""
import argparse
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import statistics

METRICS = ("taskMs", "cpuTimeMs", "peakMemoryBytes", "idleMemoryBytes", "diskReadBytes", "diskWriteBytes", "networkBytes", "storageBytes")
QUALITY = ("runtimeSecurity", "mandatoryTests", "independentBehavior", "changeScope", "completeAudit", "correctVerdicts", "deliveryVerified", "manualCriteriaPreserved")


def digest(path):
    with path.open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def local_file(root, relative):
    file = root / relative
    if file.is_symlink() or not file.resolve().is_relative_to(root) or not file.is_file():
        raise ValueError("Missing/outside raw evidence")
    return file


def report(path):
    path = Path(path).resolve(); root = path.parent
    raw = json.loads(path.read_text(encoding="utf-8"))
    groups = {}; proofs = []; failed_quality = []; failed_replay = []
    for category in ("samples", "replays"):
        for sample in raw.get(category, []):
            evidence = local_file(root, sample["evidence"])
            result = json.loads(evidence.read_text(encoding="utf-8"))
            identifier = sample.get("id", evidence.parent.name)
            if category == "replays":
                operations = result.get("operations", [])
                valid = (result.get("kind") == "operation_replay" and result.get("modelUsed") is False
                         and result.get("integrity", {}).get("incomplete") is False and bool(operations)
                         and all(operation.get("outcome", {}).get("ExitCode") == 0
                                 and operation.get("outcome", {}).get("TimedOut") is False
                                 and operation.get("outcome", {}).get("AuditIncomplete") is False
                                 and operation.get("error") == "<nil>" for operation in operations))
                if sample.get("trialFailure") or not valid:
                    failed_replay.append(identifier)
            elif sample.get("trialFailure") or any(sample.get("quality", {}).get(key) is not True for key in QUALITY):
                failed_quality.append(identifier)
            resource = local_file(root, str(evidence.parent.relative_to(root) / sample["resourceTrace"]))
            trace = json.loads(resource.read_text(encoding="utf-8"))
            if trace.get("coverage") != "whole_environment" or len(trace.get("frames", [])) < 2:
                raise ValueError("Incomplete resource trace")
            values = dict(sample)
            start = dt.datetime.fromisoformat(sample["approvedAt"].replace("Z", "+00:00"))
            finish = dt.datetime.fromisoformat(sample["deliveredAt"].replace("Z", "+00:00"))
            values["taskMs"] = (finish - start).total_seconds() * 1000 - sample.get("humanWaitMs", 0)
            if values["taskMs"] <= 0:
                raise ValueError("Invalid observation time bounds")
            cell = "/".join((sample["configuration"], sample["project"], sample["regime"]))
            group = groups.setdefault(cell, dict(kind=sample["kind"], observations=[], regimeDefinition=sample["regimeDefinition"]))
            if group["kind"] != sample["kind"] or group["regimeDefinition"] != sample["regimeDefinition"]:
                raise ValueError("Observation definitions differ within cell")
            group["observations"].append(values)
            proofs.append(dict(evidence=sample["evidence"], evidenceDigest=digest(evidence), resourceTrace=str(resource.relative_to(root)), resourceDigest=digest(resource)))
    for group in groups.values():
        observations = group.pop("observations")
        group["count"] = len(observations)
        group["metrics"] = {}
        for name in METRICS:
            values = [observation[name] for observation in observations]
            if any(type(value) not in (float, int) or not math.isfinite(value) or value < 0 for value in values):
                raise ValueError("Invalid resource metric")
            group["metrics"][name] = dict(median=statistics.median(values), min=min(values), max=max(values))
    return dict(schema=1, rawSamplesDigest=digest(path), cells=groups, evidence=proofs,
                failures=raw.get("failures", []), fullQuestCount=len(raw.get("samples", [])), replayCount=len(raw.get("replays", [])),
                failedQualityTrials=failed_quality, failedReplayTrials=failed_replay,
                qualification="not_evaluated; use the strict full-quest comparator with a complete Docker bind/shadow baseline",
                performanceWinner=None, accelerationConfirmed=False,
                limits=["operation replay excludes the model and cannot qualify full-task acceleration",
                        "working-set peaks are sampled at 750 ms",
                        "host logical IO and guest block IO have different meanings; inspect raw counters",
                        "installation and human decision wait are separate from task cost"], defaults="bind/shadow; opt-in only")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("raw_samples")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = Path(args.output)
    with output.open("x", encoding="utf-8") as stream:
        json.dump(report(args.raw_samples), stream, indent=2)
    print("Observation report: " + str(output), flush=True)
