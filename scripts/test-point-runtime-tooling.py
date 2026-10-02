"""Offline invariants for pack supply chain and whole-environment accounting."""
import hashlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import time
from pathlib import Path
import tarfile
import tempfile
import unittest

from point_runtime_metrics import Job, counter_delta, summarize_frames
from point_runtime_baseline import baseline_environment
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("point_pack", Path(__file__).with_name("build-point-runtime-wsl-pack.py"))
pack = importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(pack)
REPORT_SPEC = importlib.util.spec_from_file_location("runtime_report", Path(__file__).with_name("report-point-runtime-matrix.py"))
runtime_report = importlib.util.module_from_spec(REPORT_SPEC); REPORT_SPEC.loader.exec_module(runtime_report)


class ToolingTests(unittest.TestCase):
    def test_observation_report_preserves_uncertainty_and_refuses_outside_evidence(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); trial = root / "trial"; trial.mkdir()
            result = dict(kind="operation_replay", modelUsed=False, integrity=dict(incomplete=False),
                          operations=[dict(error="<nil>", outcome=dict(ExitCode=0, TimedOut=False, AuditIncomplete=False))])
            evidence = trial / "evidence.json"; evidence.write_text(json.dumps(result), encoding="utf-8")
            (trial / "resources.json").write_text(json.dumps(dict(coverage="whole_environment", frames=[{}, {}])), encoding="utf-8")
            sample = dict(kind="operation_replay", evidence="trial/evidence.json", resourceTrace="resources.json",
                          approvedAt="2026-10-02T00:00:00Z", deliveredAt="2026-10-02T00:00:02Z",
                          configuration="candidate", project="go", regime="warm", regimeDefinition="fixed")
            sample.update({key: 1 for key in runtime_report.METRICS if key != "taskMs"})
            raw = root / "raw.json"
            raw.write_text(json.dumps(dict(samples=[], replays=[sample], failures=[dict(reason="baseline absent")])), encoding="utf-8")
            summary = runtime_report.report(raw)
            self.assertIsNone(summary["performanceWinner"])
            self.assertFalse(summary["accelerationConfirmed"])
            self.assertEqual(summary["fullQuestCount"], 0)
            self.assertEqual(summary["failedReplayTrials"], [])
            self.assertEqual(summary["cells"]["candidate/go/warm"]["metrics"]["taskMs"]["median"], 2000)
            self.assertEqual(len(summary["failures"]), 1)
            result["operations"][0]["outcome"]["AuditIncomplete"] = True
            evidence.write_text(json.dumps(result), encoding="utf-8")
            self.assertEqual(runtime_report.report(raw)["failedReplayTrials"], ["trial"])
            sample["cpuTimeMs"] = float("nan")
            raw.write_text(json.dumps(dict(samples=[], replays=[sample])), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "resource metric"):
                runtime_report.report(raw)
            sample["cpuTimeMs"] = 1
            outside = root.parent / (root.name + "-outside.json")
            outside.write_text("{}", encoding="utf-8")
            try:
                sample["evidence"] = str(outside)
                raw.write_text(json.dumps(dict(samples=[], replays=[sample])), encoding="utf-8")
                with self.assertRaisesRegex(ValueError, "outside"):
                    runtime_report.report(raw)
            finally:
                outside.unlink()

    def test_unknown_full_quest_quality_is_never_a_pass(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "evidence.json").write_text("{}", encoding="utf-8")
            (root / "resources.json").write_text(json.dumps(dict(coverage="whole_environment", frames=[{}, {}])), encoding="utf-8")
            sample = dict(id="unknown", kind="full_quest", evidence="evidence.json", resourceTrace="resources.json",
                          approvedAt="2026-10-02T00:00:00Z", deliveredAt="2026-10-02T00:00:02Z",
                          configuration="candidate", project="go", regime="warm", regimeDefinition="fixed", quality={})
            sample.update({key: 1 for key in runtime_report.METRICS if key != "taskMs"})
            raw = root / "raw.json"; raw.write_text(json.dumps(dict(samples=[sample], replays=[])), encoding="utf-8")
            self.assertEqual(runtime_report.report(raw)["failedQualityTrials"], ["unknown"])

    @unittest.skipUnless(os.name == "nt", "Windows baseline paths")
    def test_baseline_cannot_attribute_remote_engines_or_unrelated_storage(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {"LOCALAPPDATA": temporary}):
            root = Path(temporary); disk = root / "Docker/wsl/disk/data.vhdx"
            disk.parent.mkdir(parents=True); disk.touch()
            config = dict(dockerContext="desktop-linux", resourceGuest="docker-desktop", storagePaths=[str(disk)])
            with patch.dict(os.environ, {"DOCKER_HOST": "tcp://example.invalid:2375"}):
                env = baseline_environment(config)
                self.assertNotIn("DOCKER_HOST", env)
                self.assertEqual(env["DOCKER_CONTEXT"], "desktop-linux")
                self.assertEqual(os.environ["DOCKER_HOST"], "tcp://example.invalid:2375")
            with self.assertRaises(ValueError): baseline_environment(dict(config, dockerContext="remote"))
            with self.assertRaises(ValueError): baseline_environment(dict(config, storagePaths=[str(root / "foreign.vhdx")]))
            with self.assertRaises(ValueError): baseline_environment(dict(config, resourceGuest="Ubuntu"))

    @unittest.skipUnless(os.name == "nt" and os.environ.get("POINT_METRICS_NATIVE") == "1", "explicit native job accounting test")
    def test_windows_job_counts_exited_children(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); gate = root / "gate"; done = root / "done"
            workload = "import pathlib,time,sys; end=time.monotonic()+.25\nwhile time.monotonic()<end: sum(i*i for i in range(1000))\npathlib.Path(sys.argv[1]).write_bytes(b'x'*2097152)"
            parent = "import pathlib,time,subprocess,sys\ngate=pathlib.Path(sys.argv[1])\nwhile not gate.exists(): time.sleep(.05)\nsubprocess.run([sys.executable,'-c',sys.argv[3],sys.argv[4]],check=True)\npathlib.Path(sys.argv[2]).touch()\ntime.sleep(60)"
            child = subprocess.Popen([sys.executable,"-c",parent,str(gate),str(done),workload,str(root/"data")],creationflags=0x08000000)
            job = None
            try:
                job = Job(child.pid); before = job.accounting(); gate.touch()
                deadline = time.monotonic()+15
                while not done.exists() and time.monotonic()<deadline: time.sleep(.05)
                self.assertTrue(done.exists())
                after = job.accounting()
                self.assertGreater(after["cpuTimeMs"]-before["cpuTimeMs"],5)
                self.assertGreaterEqual(after["diskWriteBytes"]-before["diskWriteBytes"],2097152)
                self.assertGreaterEqual(after["totalProcesses"],2)
            finally:
                if job: job.close()
                if child.poll() is None: child.terminate()
                child.wait(timeout=15)

    def test_owned_guest_configuration_precedes_first_boot(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); source = root / "source.tar"; target = root / "owned.tar"
            with tarfile.open(source, "w") as archive:
                for name, body in (("etc/wsl.conf", b"[automount]\nenabled=true\n"), ("etc/os-release", b"ID=fixture\n")):
                    entry = tarfile.TarInfo(name); entry.size = len(body); entry.mode = 0o644
                    archive.addfile(entry, io.BytesIO(body))
                link = tarfile.TarInfo("etc/linked"); link.type = tarfile.SYMTYPE; link.linkname = "../outside"
                archive.addfile(link)
            pack.prepare_rootfs(source, target, pack.sha256(source))
            with tarfile.open(target) as archive:
                self.assertEqual([n for n in archive.getnames() if n == "etc/wsl.conf"], ["etc/wsl.conf"])
                self.assertEqual(archive.extractfile("etc/wsl.conf").read(), pack.CONF)
                self.assertEqual(archive.getmember("etc/linked").linkname, "../outside")
            self.assertFalse((root / "etc").exists())
            with self.assertRaisesRegex(ValueError, "checksum"):
                pack.prepare_rootfs(source, root / "refused.tar", "sha256:" + "0" * 64)
            self.assertFalse((root / "refused.tar").exists())

    def test_short_lived_children_and_shared_vm_are_counted_once(self):
        before = dict(cpuTimeMs=10, diskReadBytes=100, diskWriteBytes=200, totalProcesses=1)
        after = dict(cpuTimeMs=210, diskReadBytes=400, diskWriteBytes=600, totalProcesses=3)
        def process(pid, name, cpu, memory):
            return dict(pid=pid, name=name, cpu100ns=str(cpu), workingSet=str(memory))
        frames = [dict(jobPids=[1], processes=[process(1,"core",100000,50), process(2,"vmmemWSL",1000000,500)]),
                  dict(jobPids=[1,3], processes=[process(1,"core",900000,80),process(2,"vmmemWSL",3000000,700),process(3,"point-runtime",500000,20)])]
        result = summarize_frames(frames, before, after)
        self.assertEqual(result["cpuTimeMs"],400)
        self.assertEqual(result["peakMemoryBytes"],800)
        self.assertEqual(result["idleMemoryBytes"],550)
        self.assertEqual(result["diskReadBytes"],300)

    def test_missing_or_reset_counters_never_become_zero(self):
        with self.assertRaises(ValueError): counter_delta(dict(cpuTimeMs=5),dict(cpuTimeMs=4))
        with self.assertRaises(ValueError): counter_delta(dict(cpuTimeMs=5),{})
        with self.assertRaises(ValueError): summarize_frames([],{}, {})
        frames=[dict(jobPids=[],processes=[]),dict(jobPids=[],processes=[])]
        with self.assertRaisesRegex(ValueError,"not observed"):summarize_frames(frames,dict(cpuTimeMs=0),dict(cpuTimeMs=1))


if __name__ == "__main__":
    unittest.main()
