#!/usr/bin/env python3
"""Harness contract tests; no Docker engine, product tests, or third-party modules."""

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("qualification", Path(__file__).with_name("dhcp-qualification.py"))
qualification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualification)


def stream(*events):
    return "\n".join(json.dumps(event) for event in events) + "\n"


def completed(name, action="pass", reason=None):
    events = [{"Action": "run", "Test": name}]
    if reason:
        events.append({"Action": "output", "Test": name, "Output": reason})
    return events + [{"Action": action, "Test": name}]


class ReportingTests(unittest.TestCase):
    def test_selected_tests_and_explicit_deferral_pass(self):
        text = stream(*completed("TestSocket"), *completed("TestResource", "skip", "resource deferred\n"),
                      {"Action": "pass"})
        result = qualification.test_results(text, 0, {"TestResource"}, {"TestSocket"})
        self.assertEqual(result["status"], "PASS")
        self.assertEqual(result["passed_tests"], 1)
        self.assertEqual(result["skip_reasons"], {"TestResource": "resource deferred"})

    def test_product_failure_is_retained_even_with_zero_exit(self):
        text = stream(*completed("TestSocket", "fail"), {"Action": "fail"})
        result = qualification.test_results(text, 0, required_tests={"TestSocket"})
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["failed_tests"], ["TestSocket"])

    def test_unexpected_skip_cannot_be_success(self):
        text = stream(*completed("TestHealthy"), *completed("TestSocket", "skip", "missing ip utility"),
                      {"Action": "pass"})
        result = qualification.test_results(text, 0)
        self.assertEqual(result["status"], "BLOCKED")
        self.assertEqual(result["unexpected_skips"], ["TestSocket"])
        self.assertIn("missing ip utility", result["skip_reasons"]["TestSocket"])

    def test_missing_selected_test_cannot_be_success(self):
        text = stream(*completed("TestHealthy"), {"Action": "pass"})
        result = qualification.test_results(text, 0, required_tests={"TestSocket"})
        self.assertEqual(result["status"], "BLOCKED")
        self.assertEqual(result["missing_tests"], ["TestSocket"])

    def test_required_test_cannot_be_waived_by_skip_allowlist(self):
        text = stream(*completed("TestHealthy"), *completed("TestSocket", "skip"), {"Action": "pass"})
        result = qualification.test_results(text, 0, {"TestSocket"}, {"TestSocket"})
        self.assertEqual(result["status"], "BLOCKED")
        self.assertEqual(result["incomplete_tests"], ["TestSocket"])

    def test_truncated_or_empty_output_is_incomplete(self):
        for text in ("", stream({"Action": "run", "Test": "TestSocket"}),
                     stream(*completed("TestSocket")), "not JSON\n"):
            with self.subTest(text=text):
                self.assertEqual(qualification.test_results(text, 0)["status"], "BLOCKED")


class DeadlineTests(unittest.TestCase):
    def test_timeout_removes_only_owned_container_and_next_phase_runs(self):
        report = {"run_id": "unique-run", "phases": []}
        calls = []

        def docker(command, **kwargs):
            calls.append(command)
            self.assertGreater(kwargs["timeout"], 0)
            if command[:3] == ["docker", "rm", "-f"]:
                return subprocess.CompletedProcess(command, 0, "removed", "")
            if "first" in command:
                kwargs["stdout"].write(stream({"Action": "run", "Test": "TestSocket"}))
                raise subprocess.TimeoutExpired(command, kwargs["timeout"])
            kwargs["stdout"].write(stream(*completed("TestHealthy"), {"Action": "pass"}))
            return subprocess.CompletedProcess(command, 0)

        with tempfile.TemporaryDirectory() as directory, patch.object(qualification.subprocess, "run", side_effect=docker):
            output = Path(directory)
            self.assertFalse(qualification.run_phase(output, report, "first", ["docker", "run", "first"],
                                                     timeout=1, tests=True, required_tests={"TestSocket"}))
            self.assertTrue(qualification.run_phase(output, report, "second", ["docker", "run", "second"],
                                                    timeout=1, tests=True))
        self.assertEqual(report["phases"][0]["status"], "BLOCKED")
        self.assertTrue(report["phases"][0]["timed_out"])
        self.assertEqual(report["phases"][0]["incomplete_tests"], ["TestSocket"])
        self.assertEqual(calls[1], ["docker", "rm", "-f", "dimsum-dhcp-unique-run-first"])
        self.assertEqual(calls[2][2:4], ["--name", "dimsum-dhcp-unique-run-second"])

    def test_interruption_cleans_up_and_records_partial_phase(self):
        report = {"run_id": "interrupted", "phases": []}
        with tempfile.TemporaryDirectory() as directory, patch.object(
                qualification.subprocess, "run", side_effect=[KeyboardInterrupt,
                    subprocess.CompletedProcess([], 0, "removed", "")]) as run:
            with self.assertRaises(KeyboardInterrupt):
                qualification.run_phase(Path(directory), report, "linux-dhcp", ["docker", "run", "image"], timeout=1)
        self.assertTrue(report["phases"][0]["interrupted"])
        self.assertEqual(run.call_args.args[0], ["docker", "rm", "-f", "dimsum-dhcp-interrupted-linux-dhcp"])

    def test_cleanup_timeout_is_recorded_without_hiding_original_timeout(self):
        report = {"run_id": "cleanup", "phases": []}
        with tempfile.TemporaryDirectory() as directory, patch.object(
                qualification.subprocess, "run", side_effect=subprocess.TimeoutExpired("docker", 1)):
            self.assertFalse(qualification.run_phase(Path(directory), report, "phase", ["docker", "run", "image"], timeout=1))
        self.assertTrue(report["phases"][0]["timed_out"])
        self.assertIn("error", report["phases"][0]["cleanup"])

    def test_real_outer_deadline_bounds_a_non_go_process(self):
        report = {"run_id": "real", "phases": []}
        with tempfile.TemporaryDirectory() as directory:
            result = qualification.run_phase(Path(directory), report, "sleep",
                [sys.executable, "-c", "import time; time.sleep(60)"], timeout=0.05)
        self.assertFalse(result)
        self.assertTrue(report["phases"][0]["timed_out"])
        self.assertLess(report["phases"][0]["duration_seconds"], 5)

    def test_main_preserves_reports_after_ctrl_c(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            output = root / "docs" / "interrupted"
            with patch.object(qualification, "ROOT", root), \
                    patch.object(sys, "argv", ["qualification", "--output", str(output)]), \
                    patch.object(qualification.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)), \
                    patch.object(qualification, "capture", side_effect=KeyboardInterrupt):
                self.assertEqual(qualification.main(), 1)
            report = json.loads((output / "report.json").read_text())
            self.assertEqual(report["status"], "BLOCKED")
            self.assertIn("Ctrl-C", (output / "summary.md").read_text())


class StressTests(unittest.TestCase):
    def test_all_stress_modes_are_required_bounded_and_continue_after_failure(self):
        calls = []

        def phase(name, command, **options):
            calls.append((name, command, options))
            return False  # Every failed mode must still allow independent modes.

        qualification.run_stress(phase, ["docker", "run", "--network", "none"], "fixture-image")
        self.assertEqual([name for name, _, _ in calls], ["stress-plateau", "stress-reconnect128",
            "stress-renewal", "stress-random1024", "stress-random4096", "stress-slow", "stress-failed"])
        for name, command, options in calls:
            with self.subTest(name=name):
                self.assertTrue(options["tests"])
                self.assertGreater(options["timeout"], 60)
                self.assertLessEqual(options["timeout"], 900)
                self.assertIn("DIMSUM_DHCP_QUALIFY=1", command)
                self.assertIn("DIMSUM_DHCP_SAMPLE_SECONDS=5", command)
                self.assertIn("DIMSUM_DNS_QUERY=mixed", command)
                self.assertIn("DIMSUM_DNS_QPS=1000", command)
                test = "TestQualificationStatePlateau" if name == "stress-plateau" else "TestDHCPQualificationCoexistence"
                self.assertEqual(options["required_tests"], {test})
                self.assertIn("^" + test + "$", command)
                if name != "stress-plateau":
                    self.assertIn("DIMSUM_DHCP_MODE=" + name.removeprefix("stress-"), command)


if __name__ == "__main__":
    unittest.main()
