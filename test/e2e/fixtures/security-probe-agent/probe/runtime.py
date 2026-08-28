"""Runtime-hardening probes that never return secret or host data."""

from __future__ import annotations

import os
import tempfile
from pathlib import Path


def _proc_status() -> dict[str, str]:
    values: dict[str, str] = {}
    try:
        for line in Path("/proc/self/status").read_text(encoding="utf-8").splitlines():
            key, separator, value = line.partition(":")
            if separator:
                values[key] = value.strip()
    except OSError:
        return {}
    return values


def runtime_posture() -> dict[str, object]:
    """Return booleans describing the sandbox without exposing its contents."""

    root_filesystem_read_only = False
    # This directory is owned by the probe's non-root UID in the image. A write
    # therefore succeeds on a writable root filesystem and fails only when the
    # workload's root filesystem is actually mounted read-only.
    root_probe = Path("/security-probe-fs-test/write-check")
    try:
        root_probe.write_text("probe", encoding="utf-8")
        root_probe.unlink(missing_ok=True)
    except OSError:
        root_filesystem_read_only = True

    tmp_writable = False
    try:
        with tempfile.NamedTemporaryFile(prefix="amp-security-probe-", dir="/tmp"):
            tmp_writable = True
    except OSError:
        pass

    status = _proc_status()
    cap_eff = status.get("CapEff", "")
    seccomp = status.get("Seccomp", "")
    no_new_privileges = status.get("NoNewPrivs", "")

    return {
        "non_root": os.geteuid() != 0,
        "root_filesystem_read_only": root_filesystem_read_only,
        "tmp_writable": tmp_writable,
        "service_account_token_present": Path(
            "/var/run/secrets/kubernetes.io/serviceaccount/token"
        ).exists(),
        "effective_capabilities_dropped": bool(cap_eff)
        and int(cap_eff, 16) == 0,
        "no_new_privileges": no_new_privileges == "1",
        "seccomp_enabled": seccomp == "2",
    }
