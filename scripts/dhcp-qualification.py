#!/usr/bin/env python3
"""Run DHCP qualification in disposable offline Docker namespaces; stdlib only."""

import argparse
import hashlib
import json
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid


ROOT = Path(__file__).resolve().parents[1]
PACKAGES = ("dhcp", "app", "config", "control", "admin", "webassets")
OPT_INS = ("ISOLATED_TEST", "UDHCPC_TEST", "NETNS_TEST", "CAPABILITY_TEST")
# Required top-level TestLinux names in the current DHCP/app suites. Update this
# explicit inventory when adding or renaming a selected Linux qualification.
LINUX_TESTS = {
    "dhcp": {
        "TestLinuxMinimalServiceCapabilities", "TestLinuxIndependentClientAvoidsARPConflict",
        "TestLinuxDiagnosticBroadcast", "TestLinuxRuntimeProcessRestart",
        "TestLinuxIndependentClientRenewRestart", "TestLinuxIsolatedDelivery",
        "TestLinuxProductionAdapter", "TestLinuxProductionRuntimeDORA",
        "TestLinuxRejectsNonPermanentAddress", "TestLinuxIndependentUDHCPC",
    },
    "app": {"TestLinuxServiceDHCPIPv4Wildcard"},
}
DEFERRED = {
    "dhcp": {"TestQualificationStatePlateau"},
    "app": {"TestDHCPQualificationCoexistence"},
    "webassets": {"TestBrowserAgainstGoAPI", "TestBrowserAgainstManagedRuntime"},
}
# Outer deadlines also bound Docker startup and Go compilation, unlike -timeout.
INSPECT_SECONDS, BUILD_SECONDS, PREFLIGHT_SECONDS, TEST_SECONDS = 30, 1200, 60, 900
CLEANUP_SECONDS = 30
STRESS_MODES = ("reconnect128", "renewal", "random1024", "random4096", "slow", "failed")


def capture(args):
    return subprocess.check_output(args, cwd=ROOT, text=True, timeout=INSPECT_SECONDS).strip()


def test_results(text, exit_code, allowed_skips=(), required_tests=()):
    """A zero exit is insufficient: selected tests must complete and skips are explicit."""
    ran, outcomes, outputs, package_outcomes = set(), {}, {}, []
    malformed = 0
    for line in text.splitlines():
        try:
            event = json.loads(line)
            if not isinstance(event, dict):
                raise ValueError("not an event")
        except ValueError:
            malformed += 1
            continue
        action, test = event.get("Action"), event.get("Test")
        if test:
            if action == "run":
                ran.add(test)
            if action == "output":
                outputs.setdefault(test, []).append(event.get("Output", ""))
            if action in ("pass", "fail", "skip"):
                outcomes[test] = action
        elif action in ("pass", "fail", "skip"):
            package_outcomes.append(action)
    failed = sorted(t for t, action in outcomes.items() if action == "fail")
    skipped = sorted(t for t, action in outcomes.items() if action == "skip")
    passed = {t for t, action in outcomes.items() if action == "pass"}
    missing = sorted(set(required_tests) - ran)
    incomplete = sorted((ran - outcomes.keys()) | (set(required_tests) - passed - set(failed)))
    unexpected = sorted(set(skipped) - set(allowed_skips))
    status = "PASS"
    if exit_code != 0 or failed or "fail" in package_outcomes:
        status = "FAIL"
    if (missing or incomplete or unexpected or malformed or not package_outcomes
            or "skip" in package_outcomes or not passed and not failed):
        status = "BLOCKED"
    return {"status": status, "failed_tests": failed, "skipped_tests": skipped,
            "skip_reasons": {t: "".join(outputs.get(t, [])).strip() or "No skip reason emitted"
                             for t in skipped},
            "unexpected_skips": unexpected, "missing_tests": missing,
            "incomplete_tests": incomplete, "malformed_json_lines": malformed,
            "passed_tests": len(passed)}


def run_phase(output, report, name, command, *, timeout, tests=False,
              allowed_skips=(), required_tests=()):
    command = list(command)
    container = None
    if command[:2] == ["docker", "run"]:
        container = f"dimsum-dhcp-{report['run_id']}-{name}"
        command[2:2] = ["--name", container]
    print(f"{name}: {shlex.join(command)}", flush=True)
    begin = time.monotonic()
    row = {"name": name, "command": shlex.join(command), "timeout_seconds": timeout,
           "container": container, "exit_code": None, "status": "BLOCKED",
           "timed_out": False, "interrupted": False,
           "failed_tests": [], "skipped_tests": [], "passed_tests": 0,
           "skip_reasons": {}, "unexpected_skips": [], "missing_tests": [], "incomplete_tests": []}
    log = output / (name + (".jsonl" if tests else ".log"))
    try:
        with log.open("w") as stdout, (output / (name + ".stderr.log")).open("w") as stderr:
            result = subprocess.run(command, cwd=ROOT, stdout=stdout, stderr=stderr, timeout=timeout)
        row["exit_code"] = result.returncode
        row["status"] = "PASS" if result.returncode == 0 else "FAIL"
    except subprocess.TimeoutExpired:
        row["timed_out"] = True
        row["blocked_reason"] = f"Outer deadline exceeded ({timeout}s)"
    except KeyboardInterrupt:
        row["interrupted"] = True
        row["blocked_reason"] = "Interrupted by Ctrl-C"
    except OSError as error:
        row["blocked_reason"] = str(error)
    finally:
        if container and (row["timed_out"] or row["interrupted"]):
            # A killed Docker client can leave its container running. Only remove
            # this invocation's named container; never touch another run.
            previous = signal.signal(signal.SIGINT, signal.SIG_IGN)
            try:
                cleanup = subprocess.run(["docker", "rm", "-f", container], cwd=ROOT,
                                         capture_output=True, text=True, timeout=CLEANUP_SECONDS)
                row["cleanup"] = {"exit_code": cleanup.returncode,
                                  "output": cleanup.stdout + cleanup.stderr}
            except (OSError, subprocess.TimeoutExpired) as error:
                row["cleanup"] = {"error": str(error)}
            finally:
                signal.signal(signal.SIGINT, previous)
        if tests and log.exists():
            result = test_results(log.read_text(), row["exit_code"], allowed_skips, required_tests)
            row.update(result)
        if row.get("blocked_reason"):
            row["status"] = "BLOCKED"
        row["duration_seconds"] = round(time.monotonic() - begin, 3)
        report["phases"].append(row)
    print(f"{name}: {row['status']} ({row['duration_seconds']}s)", flush=True)
    if row["interrupted"]:
        raise KeyboardInterrupt
    return row["status"] == "PASS"


def run_stress(phase, docker, image):
    """Independent, bounded fixture loads; reuse the normal phase/report path."""
    options = ["-e", "DIMSUM_DHCP_QUALIFY=1", "-e", "DIMSUM_DHCP_SAMPLE_SECONDS=5",
               "-e", "DIMSUM_DNS_QUERY=mixed", "-e", "DIMSUM_DNS_QPS=1000"]
    cases = [("plateau", "dhcp", "TestQualificationStatePlateau", [])]
    cases += [(mode, "app", "TestDHCPQualificationCoexistence", ["-e", "DIMSUM_DHCP_MODE=" + mode])
              for mode in STRESS_MODES]
    for name, package, test, mode_options in cases:
        phase("stress-" + name, docker + options + mode_options + [image, "go", "test", "-json",
              "-count=1", "-p", "1", "-timeout", "10m", "-run", "^" + test + "$", "./internal/" + package],
              tests=True, timeout=TEST_SECONDS, required_tests={test})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path,
                        help="new, Git-ignored directory under docs/ (must not exist)")
    parser.add_argument("--stress", action="store_true",
                        help="also run bounded state-plateau and six injected-client/DNS fixture loads")
    args = parser.parse_args()
    output = args.output.resolve()
    if not output.is_relative_to(ROOT / "docs") or output == ROOT / "docs":
        parser.error("--output must be a new directory beneath this checkout's docs/")
    if output.exists():
        parser.error("output already exists; choose a new run directory")
    if subprocess.run(["git", "check-ignore", "-q", str(output)], cwd=ROOT,
                      timeout=INSPECT_SECONDS).returncode:
        parser.error("output must be Git-ignored")
    output.mkdir(parents=True)
    started = time.monotonic()
    report = {"commit": None, "dirty_status": None, "run_id": uuid.uuid4().hex[:12], "stress": args.stress,
              "invocation": shlex.join([sys.executable, *sys.argv]),
              "image_id": None, "phases": [], "status": "BLOCKED"}

    def phase(name, command, **options):
        return run_phase(output, report, name, command, **options)

    try:
        report["commit"] = capture(["git", "rev-parse", "HEAD"])
        report["dirty_status"] = capture(["git", "status", "--short"])
        assets = ROOT / "internal/webassets/dist"
        if not (assets / "index.html").is_file():
            raise RuntimeError("missing generated UI: run sh scripts/build-web.sh first")
        if not phase("docker-inspect", ["docker", "version", "--format", "{{json .Server}}"],
                     timeout=INSPECT_SECONDS):
            raise RuntimeError("Docker inspection failed")
        report["docker_server"] = json.loads((output / "docker-inspect.log").read_text())
        if report["docker_server"]["Os"] != "linux":
            raise RuntimeError("Docker server must run Linux")
        with tempfile.TemporaryDirectory(prefix="dimsum-dhcp-") as temporary:
            context = Path(temporary)
            paths = subprocess.check_output(
                ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
                cwd=ROOT, timeout=INSPECT_SECONDS
            ).decode().split("\0")
            paths += [str(p.relative_to(ROOT)) for p in assets.rglob("*") if p.is_file()]
            manifest = {}
            for relative in sorted(set(paths) - {""}):
                source = ROOT / relative
                if not source.exists():  # Include working-tree deletions in the snapshot.
                    continue
                if source.is_symlink() or not source.is_file():
                    raise RuntimeError(f"unsupported snapshot entry: {relative}")
                target = context / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
                manifest[relative] = hashlib.sha256(target.read_bytes()).hexdigest()
            (output / "snapshot-sha256.json").write_text(json.dumps(manifest, indent=2) + "\n")
            # Explicit invocation-local build context avoids ignored private files and sockets.
            if not phase("image-build", ["docker", "build", "--iidfile", str(output / "image.id"),
                                         "-f", str(context / "scripts/dhcp-qualification.Dockerfile"), str(context)],
                         timeout=BUILD_SECONDS):
                raise RuntimeError("qualification image build failed; see image-build logs")
        image = (output / "image.id").read_text().strip()
        report["image_id"] = image
        docker = ["docker", "run", "--rm", "--network", "none", "--cap-drop", "ALL"]
        for capability in ("NET_ADMIN", "NET_RAW", "NET_BIND_SERVICE", "SYS_ADMIN", "SETPCAP"):
            docker += ["--cap-add", capability]
        docker += ["--security-opt", "seccomp=unconfined"]
        for setting in ("net.ipv4.conf.default.accept_local=1", "net.ipv4.conf.all.rp_filter=0",
                        "net.ipv4.conf.default.rp_filter=0", "net.ipv4.ip_unprivileged_port_start=1024"):
            docker += ["--sysctl", setting]
        preflight = ('set -eux; test "$(go env GOVERSION)" = go1.26.8; '
                     'test -z "$(ip -o addr show scope global)"; test -z "$(ip route show default)"; '
                     'busybox --list | grep -qx udhcpc; command -v nsenter; '
                     'command -v setpriv; ip link set lo up; '
                     'ip link add qualify0 type veth peer name qualify1; ip link del qualify0; '
                     'unshare --net sh -c "ip link set lo up"; '
                     'go version; uname -a; dpkg-query -W iproute2 busybox procps util-linux')
        if not phase("prerequisites", docker + [image, "sh", "-c", preflight], timeout=PREFLIGHT_SECONDS):
            raise RuntimeError("isolated container prerequisites failed")
        for package in PACKAGES:
            phase("unit-" + package, docker + [image, "go", "test", "-json", "-count=1",
                                               "-p", "1", "-timeout", "10m", "./internal/" + package],
                  tests=True, timeout=TEST_SECONDS,
                  allowed_skips=DEFERRED.get(package, set()) | LINUX_TESTS.get(package, set()))
        for package in ("dhcp", "app"):
            options = sum((["-e", "DIMSUM_DHCP_" + opt + "=1"] for opt in OPT_INS), [])
            phase("linux-" + package, docker + options + [image, "go", "test", "-json",
                  "-count=1", "-p", "1", "-timeout", "10m", "-run", "^TestLinux", "./internal/" + package],
                  tests=True, timeout=TEST_SECONDS, required_tests=LINUX_TESTS[package])
        if args.stress:
            run_stress(phase, docker, image)
        statuses = {p["status"] for p in report["phases"]}
        report["status"] = "FAIL" if "FAIL" in statuses else "BLOCKED" if "BLOCKED" in statuses else "PASS"
    except KeyboardInterrupt:
        report["blocked_reason"] = "Interrupted by Ctrl-C; partial results preserved"
    except (OSError, RuntimeError, subprocess.SubprocessError, ValueError) as error:
        report["blocked_reason"] = str(error)
        print(f"BLOCKED: {error}", file=sys.stderr)
    finally:
        report["duration_seconds"] = round(time.monotonic() - started, 3)
        (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        lines = ["# DHCP qualification", "", f"Overall: **{report['status']}**", "",
                 f"- Commit: `{report['commit']}`",
                 f"- Image: `{report['image_id']}`",
                 f"- Duration: {report['duration_seconds']} seconds",
                 f"- Invocation: `{report['invocation']}`", "", "## Working tree", "",
                  "```text", report["dirty_status"] if report["dirty_status"] is not None else "unknown", "```", "",
                 "## Scope", "",
                 "Go 1.26.8; fresh offline Docker network namespace per phase; no host mounts.",
                 ("Stress selected: state plateau plus reconnect128, renewal, random1024, random4096, slow, failed."
                  if args.stress else
                  "SKIP / DEFERRED: `TestQualificationStatePlateau` and `TestDHCPQualificationCoexistence` (enable --stress)."),
                 ("Stress uses 5-second samples, mixed DNS queries at target 1000 QPS; reconnect may take 60 seconds. "
                  "DHCP wire/probe fixtures are injected; DNS uses real UDP and the real store. "
                  "These are fixture stress results, not physical-wire or Raspberry Pi benchmarks."
                  if args.stress else "Resource qualification is not enabled."),
                 "`DIMSUM_DHCP_SERVICE_TEST` and `DIMSUM_DHCP_PROFILE_DIR` are not enabled.",
                 "Capability-drop tests use SETPCAP and privileged-port threshold 1024.",
                 "Child selector variables `CAP_CHILD` and `SERVER_CHILD` are owned by tests.",
                 "Browser opt-in tests are not enabled. Skips below are not passes.", ""]
        if report.get("blocked_reason"):
            lines += ["## BLOCKED", "", report["blocked_reason"], ""]
        for row in report["phases"]:
            lines += [f"## {row['name']}: {row['status']}", "",
                      f"Duration: {row['duration_seconds']}s; exit: {row['exit_code']}; passed tests: {row['passed_tests']}",
                      "", "```sh", row["command"], "```", ""]
            if row.get("blocked_reason"):
                lines += [f"BLOCKED: {row['blocked_reason']}", ""]
            if row.get("cleanup"):
                lines += ["Container cleanup: `" + json.dumps(row["cleanup"]) + "`", ""]
            for key, label in (("failed_tests", "FAIL"), ("skipped_tests", "SKIP"),
                               ("unexpected_skips", "BLOCKED unexpected skip"),
                               ("missing_tests", "BLOCKED missing test"),
                               ("incomplete_tests", "BLOCKED incomplete test")):
                lines += [f"- {label}: `{name}`" for name in row[key]]
            for name, reason in row["skip_reasons"].items():
                lines += [f"\nSkip output for `{name}`:", "```text", reason, "```"]
            lines.append("")
        (output / "summary.md").write_text("\n".join(lines))
    print(f"Report: {output / 'summary.md'}")
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
