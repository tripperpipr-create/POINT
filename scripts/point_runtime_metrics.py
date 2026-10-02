"""Whole Point environment accounting on Windows; no third-party packages.

The measured child waits for a gate before approval and after receipt. Windows
job accounting includes short-lived children; CIM includes protected WSL CPU.
Guest block-device/NIC counters account for Linux I/O. Foreign running WSL
guests invalidate a sample rather than attributing their work to Point.
"""
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import subprocess
import threading
import time

ROOT = Path(__file__).resolve().parent.parent
INFRA = ("vmmemWSL", "wslservice", "wslhost", "wslrelay", "point-runtime", "com.docker.backend", "Docker Desktop")


def atomic_json(path, value):
    path = Path(path)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value), encoding="utf-8")
    temporary.replace(path)


def running_distributions():
    raw = subprocess.check_output(["wsl.exe", "--list", "--running", "--quiet"], creationflags=0x08000000)
    value = raw.decode("utf-16le" if b"\x00" in raw else "utf-8").lstrip("\ufeff")
    return {line.strip() for line in value.splitlines() if line.strip()}


def require_owned_guest(distribution):
    foreign = running_distributions() - {distribution}
    if foreign:
        raise RuntimeError("Shared WSL metrics cannot isolate other running distributions: " + ", ".join(sorted(foreign)))


GUEST_COUNTERS = r'''import json,pathlib
p=pathlib.Path
r=w=0
for line in p('/proc/diskstats').read_text().splitlines():
 s=line.split(); name=s[2]
 if not (p('/sys/class/block')/name/'partition').exists() and not name.startswith(('loop','ram')):
  r+=int(s[5])*512; w+=int(s[9])*512
n=p('/sys/class/net/eth0/statistics')
print(json.dumps(dict(diskReadBytes=r,diskWriteBytes=w,networkBytes=int((n/'rx_bytes').read_text())+int((n/'tx_bytes').read_text()))))'''


def guest_counters(distribution):
    require_owned_guest(distribution)
    if distribution == "docker-desktop":
        # The Desktop appliance need not ship Python. No image/container is
        # started for measurement; read only its kernel counters.
        script = r'''test -s /proc/diskstats
test -r /sys/class/net/eth0/statistics/rx_bytes
rx=$(cat /sys/class/net/eth0/statistics/rx_bytes)
tx=$(cat /sys/class/net/eth0/statistics/tx_bytes)
awk -v n="$((rx+tx))" '$3 !~ /^(loop|ram)/ {p="/sys/class/block/"$3"/partition"; if ((getline v < p)<=0) {r+=$6*512;w+=$10*512};close(p)} END {printf "{\"diskReadBytes\":%.0f,\"diskWriteBytes\":%.0f,\"networkBytes\":%.0f}\n",r,w,n}' /proc/diskstats'''
        command = ["/bin/sh", "-ec", script]
    elif distribution.startswith("point-runtime-"):
        command = ["/usr/bin/python3", "-c", GUEST_COUNTERS]
    else:
        raise ValueError("Unrecognized resource guest")
    raw = subprocess.check_output(["wsl.exe", "--distribution", distribution, "--user", "root", "--exec", *command], creationflags=0x08000000, timeout=30)
    return json.loads(raw)


class BasicAccounting(ctypes.Structure):
    _fields_ = [(name, ctypes.c_int64) for name in ("user", "kernel", "periodUser", "periodKernel")] + [(name, wintypes.DWORD) for name in ("pageFaults", "totalProcesses", "activeProcesses", "terminatedProcesses")]


class IOAccounting(ctypes.Structure):
    _fields_ = [(name, ctypes.c_uint64) for name in ("readOps", "writeOps", "otherOps", "readBytes", "writeBytes", "otherBytes")]


class JobAccounting(ctypes.Structure):
    _fields_ = [("basic", BasicAccounting), ("io", IOAccounting)]


class Job:
    def __init__(self, pid):
        if os.name != "nt":
            raise RuntimeError("Windows job metrics require Windows")
        self.api = ctypes.WinDLL("kernel32", use_last_error=True)
        self.api.CreateJobObjectW.restype = wintypes.HANDLE
        self.api.OpenProcess.restype = wintypes.HANDLE
        self.api.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
        self.api.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        self.api.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        self.api.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
        self.api.QueryInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p]
        self.api.CloseHandle.argtypes = [wintypes.HANDLE]
        self.handle = self.api.CreateJobObjectW(None, None)
        limits = ctypes.create_string_buffer(144)
        ctypes.c_uint32.from_buffer(limits, 16).value = 0x2000  # KILL_ON_JOB_CLOSE
        if not self.handle or not self.api.SetInformationJobObject(self.handle, 9, limits, len(limits)):
            raise ctypes.WinError(ctypes.get_last_error())
        process = self.api.OpenProcess(0x0100 | 0x0001 | 0x1000, False, pid)
        try:
            if not self.handle or not process or not self.api.AssignProcessToJobObject(self.handle, process):
                raise ctypes.WinError(ctypes.get_last_error())
        finally:
            if process:
                self.api.CloseHandle(process)

    def accounting(self):
        value = JobAccounting()
        if not self.api.QueryInformationJobObject(self.handle, 8, ctypes.byref(value), ctypes.sizeof(value), None):
            raise ctypes.WinError(ctypes.get_last_error())
        return dict(cpuTimeMs=(value.basic.user + value.basic.kernel) / 10000,
                    diskReadBytes=value.io.readBytes, diskWriteBytes=value.io.writeBytes,
                    totalProcesses=value.basic.totalProcesses)

    def pids(self):
        buffer = ctypes.create_string_buffer(8 + 65536 * ctypes.sizeof(ctypes.c_size_t))
        if not self.api.QueryInformationJobObject(self.handle, 3, buffer, len(buffer), None):
            raise ctypes.WinError(ctypes.get_last_error())
        count = ctypes.c_uint32.from_buffer(buffer, 4).value
        return list((ctypes.c_size_t * count).from_buffer(buffer, 8))

    def close(self):
        self.api.CloseHandle(self.handle)


def counter_delta(before, after):
    result = {}
    for key in before:
        if key not in after or not isinstance(after[key], (float, int)) or after[key] < before[key]:
            raise ValueError("Missing/reset resource counter: " + key)
        result[key] = after[key] - before[key]
    return result


def summarize_frames(frames, before_job, after_job):
    if len(frames) < 2:
        raise ValueError("Resource trace is incomplete")
    baseline = {p["pid"]: int(p["cpu100ns"]) for p in frames[0]["processes"] if p["pid"] not in frames[0]["jobPids"]}
    latest = dict(baseline)
    peak = 0
    saw_guest = False
    for frame in frames:
        memory = 0
        for process in frame["processes"]:
            if process["workingSet"] in (None, "") or process["cpu100ns"] in (None, ""):
                raise ValueError("Protected process metric unavailable")
            memory += int(process["workingSet"])
            saw_guest |= process["name"].startswith("vmmemWSL")
            if process["pid"] not in frame["jobPids"]:
                value = int(process["cpu100ns"])
                if process["pid"] in latest and value < latest[process["pid"]]:
                    raise ValueError("Infrastructure PID/counter changed during measurement")
                latest[process["pid"]] = value
        peak = max(peak, memory)
    if not saw_guest:
        raise ValueError("WSL VM memory/CPU was not observed")
    delta = counter_delta(before_job, after_job)
    delta["cpuTimeMs"] += sum(value - baseline.get(pid, 0) for pid, value in latest.items()) / 10000
    delta["peakMemoryBytes"] = peak
    delta["idleMemoryBytes"] = sum(int(p["workingSet"]) for p in frames[0]["processes"])
    return delta


class Collector:
    def __init__(self, job, directory, distribution):
        self.job, self.directory, self.distribution = job, Path(directory), distribution
        self.pid_file = self.directory / "metric-pids.json"
        atomic_json(self.pid_file, job.pids())
        self.frames, self.errors = [], []
        self.lock = threading.Lock()
        self.worker = subprocess.Popen(["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(ROOT / "scripts/point-runtime-metrics.ps1"), "-PidFile", str(self.pid_file)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=0x08000000)
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        try:
            for line in self.worker.stdout:
                frame = json.loads(line.decode("utf-8-sig"))
                pids = self.job.pids()
                frame["jobPids"] = pids
                require_owned_guest(self.distribution)
                atomic_json(self.pid_file, pids)
                with self.lock:
                    self.frames.append(frame)
        except Exception as error:
            self.errors.append(str(error))

    def frame(self, timeout=30):
        with self.lock:
            count = len(self.frames)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.errors:
                raise RuntimeError(self.errors[0])
            with self.lock:
                if len(self.frames) > count:
                    return len(self.frames) - 1
            if self.worker.poll() is not None:
                raise RuntimeError("CIM worker exited: " + self.worker.stderr.read().decode(errors="replace")[-2000:])
            time.sleep(.1)
        raise TimeoutError("CIM resource frame missing")

    def stop(self):
        self.worker.terminate()
        self.worker.wait(timeout=15)
        self.reader.join(timeout=15)
        if self.errors:
            raise RuntimeError(self.errors[0])
