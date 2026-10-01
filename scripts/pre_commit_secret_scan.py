"""Pre-commit secret scan for the staged diff (local use only, not committed)."""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

PATTERNS = [
    (r"sk-(live|octopus|ant|proj)-[A-Za-z0-9_\-]{8,}", "openai-style key"),
    (r"sk-ant-[A-Za-z0-9_\-]{8,}", "anthropic key"),
    (r"xox[bap]-[A-Za-z0-9\-]{8,}", "slack token"),
    (r"ghp_[A-Za-z0-9]{8,}", "github pat"),
    (r"AIza[A-Za-z0-9_\-]{8,}", "google api key"),
    (r"AKIA[0-9A-Z]{8,}", "aws access key"),
    (r"-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----", "private key block"),
]

ALLOW = re.compile(r"(INVALID_API_KEY|DUMMY|sk-test|example|placeholder|test-key)", re.I)


def git_out(args: list[str]) -> str:
    proc = subprocess.run(
        ["git", *args],
        capture_output=True, cwd=str(ROOT),
    )
    return proc.stdout.decode("utf-8", errors="replace")


def main() -> int:
    diff = git_out(["diff", "--cached", "-U0"])
    hits: list[str] = []
    for i, line in enumerate(diff.splitlines(), 1):
        if not line.startswith("+") or line.startswith("+++"):
            continue
        body = line[1:]
        if ALLOW.search(body):
            continue
        for pat, label in PATTERNS:
            if re.search(pat, body):
                hits.append(f"L{i} [{label}] {body[:120]}")
    if hits:
        print("SECRET SCAN FAILED:")
        print("\n".join(hits[:20]))
        return 1
    staged = git_out(["diff", "--cached", "--name-only"]).splitlines()
    bad_names = [f for f in staged if re.search(r"(?i)(backup|dump).*\.json$|\.env$|\.pem$|\.key$", f)]
    if bad_names:
        print("BLOCKED FILENAMES:")
        print("\n".join(bad_names))
        return 1
    print(f"secret scan passed ({len(staged)} staged files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
