"""Source identity shared by native attestations and trial preflight."""
import hashlib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROFILE = "point-container-security-v2"


def sandbox_source_digest():
    files = [ROOT / "go.mod", ROOT / "go.sum"]
    for directory in ("internal/sandbox", "internal/osproc", "internal/sandboxsync", "internal/filepolicy", "cmd/point-sandboxd"):
        files.extend((ROOT / directory).glob("*.go"))
    digest = hashlib.sha256()
    for file in sorted(files):
        raw = file.read_bytes()
        digest.update(file.relative_to(ROOT).as_posix().encode() + b"\x00")
        digest.update(str(len(raw)).encode() + b"\x00" + raw)
    return "sha256:" + digest.hexdigest()
