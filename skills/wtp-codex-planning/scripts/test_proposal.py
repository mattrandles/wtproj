#!/usr/bin/env python3
"""Contract tests for the read-only planning helper (no live WTP access)."""

import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from proposal import METADATA_FLAGS, prepare


SCRIPT = Path(__file__).with_name("proposal.py").resolve()
SKILL = SCRIPT.parent.parent


def sample():
    reference = (SKILL / "references" / "proposal.md").read_text(encoding="utf-8")
    return json.loads(reference.split("```json\n", 1)[1].split("```", 1)[0])


def split_proposal(count=3):
    proposal = sample()
    proposal["enabledModels"]["example-fast"] = ["medium"]
    proposal["distribution"] = [
        {"model": "example-model", "effort": "high", "percent": 50},
        {"model": "example-fast", "effort": "medium", "percent": 50},
    ]
    template = proposal["tasks"][0]
    proposal["tasks"] = []
    for index in range(count):
        task = copy.deepcopy(template)
        task["number"] = index + 1
        task["title"] = "Outcome %s" % (index + 1)
        if index >= (count + 1) // 2:
            task.update(model="example-fast", effort="medium")
        proposal["tasks"].append(task)
    return proposal


class ProposalTests(unittest.TestCase):
    def test_documented_example_is_a_preview_not_approval(self):
        result = prepare(sample())
        self.assertFalse(result["handoffReady"])
        self.assertEqual(result["distribution"][0]["count"], 1)
        self.assertEqual(result["distribution"][0]["actualPercent"], 100)
        self.assertIn("no WTP commands executed", result["execution"])

    def test_invalid_distributions_require_correction(self):
        invalid = [
            ([], "non-empty"),
            ([60, 30], "sum to exactly 100"),
            ([70, 40], "sum to exactly 100"),
            ([0, 0], "sum to exactly 100"),
            ([-1, 101], "finite and between"),
            ([float("nan"), 50], "finite and between"),
            ([float("inf"), 50], "finite and between"),
            ([True, 99], "numeric"),
            ([None, 100], "numeric"),
            (["invalid", 100], "numeric"),
            (["50.00000000000000000000000000001", 50], "sum to exactly 100"),
        ]
        for percentages, message in invalid:
            with self.subTest(percentages=percentages):
                proposal = split_proposal()
                proposal["distribution"] = [
                    dict(entry, percent=percent)
                    for entry, percent in zip(proposal["distribution"], percentages)
                ]
                with self.assertRaisesRegex(ValueError, message):
                    prepare(proposal)

    def test_corrected_split_and_exact_decimal_input(self):
        proposal = split_proposal(5)
        for entry, percent in zip(proposal["distribution"], [60, 40]):
            entry["percent"] = percent
        result = prepare(proposal)
        self.assertEqual([row["quota"] for row in result["distribution"]], [3, 2])
        proposal = split_proposal(3)
        proposal["distribution"][0]["percent"] = "66.66666666666666666666666666667"
        proposal["distribution"][1]["percent"] = "33.33333333333333333333333333333"
        self.assertEqual([r["quota"] for r in prepare(proposal)["distribution"]], [2, 1])

    def test_largest_remainder_ties_follow_user_order(self):
        proposal = split_proposal()
        result = prepare(proposal)
        self.assertEqual([row["quota"] for row in result["distribution"]], [2, 1])
        self.assertAlmostEqual(result["distribution"][0]["actualPercent"], 200 / 3)
        self.assertTrue(any("Integer rounding" in warning for warning in result["warnings"]))
        proposal["distribution"].reverse()
        proposal["tasks"][1].update(model="example-fast", effort="medium")
        self.assertEqual([row["quota"] for row in prepare(proposal)["distribution"]], [2, 1])

    def test_inventory_and_pairs_must_be_explicit_and_supported(self):
        for field, value, message in [
            ("enabledModels", {}, "non-empty inventory"),
            ("enabledModels", {"example-model": []}, "supported efforts"),
            ("enabledModels", {"example-model": ["high", "high"]}, "duplicates"),
            ("distribution", [{"model": "unknown", "effort": "high", "percent": 100}], "unsupported"),
            ("distribution", [{"model": "example-model", "effort": "unknown", "percent": 100}], "unsupported"),
            ("distribution", [{"model": "example-model", "percent": 100}], "missing fields"),
            ("distribution", [{"model": "example-model", "effort": "high", "percent": 50}] * 2, "duplicate"),
        ]:
            with self.subTest(field=field, value=value):
                proposal = sample()
                proposal[field] = value
                with self.assertRaisesRegex(ValueError, message):
                    prepare(proposal)

    def test_zero_share_and_unselected_pairs_never_receive_tasks(self):
        proposal = split_proposal(2)
        for entry, percent in zip(proposal["distribution"], [100, 0]):
            entry["percent"] = percent
        proposal["distributionDeviation"] = "Risk does not authorize an unselected model."
        with self.assertRaisesRegex(ValueError, "zero-share"):
            prepare(proposal)
        proposal["distribution"].pop()
        with self.assertRaisesRegex(ValueError, "unselected"):
            prepare(proposal)
        proposal["tasks"][1].update(model="example-model", effort="high")
        self.assertEqual(prepare(proposal)["distribution"][0]["count"], 2)

    def test_same_model_with_distinct_efforts_has_separate_quotas(self):
        proposal = split_proposal(2)
        proposal["enabledModels"] = {"example-model": ["high", "medium"]}
        proposal["distribution"][1]["model"] = "example-model"
        proposal["tasks"][1]["model"] = "example-model"
        self.assertEqual([row["count"] for row in prepare(proposal)["distribution"]], [1, 1])

    def test_discretionary_deviation_requires_explanation_and_approval(self):
        proposal = split_proposal()
        proposal["tasks"][2].update(model="example-model", effort="high")
        with self.assertRaisesRegex(ValueError, "distributionDeviation"):
            prepare(proposal)
        proposal["distributionDeviation"] = "All three outcomes affect persistence atomicity."
        preview = prepare(proposal)
        self.assertFalse(preview["handoffReady"])
        self.assertEqual([row["count"] for row in preview["distribution"]], [3, 0])
        self.assertTrue(any("Quota deviation" in warning for warning in preview["warnings"]))
        self.assertTrue(prepare(proposal, "approve", preview["digest"])["handoffReady"])

    def test_metadata_roundtrip_and_unsupported_fallback(self):
        proposal = sample()
        task = proposal["tasks"][0]
        task["metadata"] = {key: "literal " + key for key in METADATA_FLAGS if key != "lane"}
        proposal["supportedMetadata"] = list(METADATA_FLAGS)
        argv = prepare(proposal)["commands"][0]["argv"]
        flags = dict(zip(argv[4::2], argv[5::2]))
        self.assertEqual(argv[:4], ["wtp", "--json", "task", "create"])
        for key, value in {"lane": task["lane"], **task["metadata"]}.items():
            self.assertEqual(flags[METADATA_FLAGS[key]], value)
        self.assertEqual(flags["--status"], "todo")
        self.assertEqual(flags["--model"], "example-model high")
        self.assertEqual(flags["--estimate"], task["estimate"])
        self.assertEqual(flags["--priority"], task["priority"])
        self.assertNotIn("--agent", argv)
        for value in (proposal["plan"]["scope"], task["description"], task["acceptance"],
                      task["verification"], task["assignmentReason"]):
            self.assertIn(value, flags["--description"])
        proposal["supportedMetadata"] = []
        result = prepare(proposal)
        argv = result["commands"][0]["argv"]
        description = argv[argv.index("--description") + 1]
        for key, value in {"lane": task["lane"], **task["metadata"]}.items():
            self.assertNotIn(METADATA_FLAGS[key], argv)
            self.assertIn(key + ": " + value, description)
        self.assertEqual(len(result["warnings"]), len(METADATA_FLAGS))

    def test_literal_shell_characters_remain_argument_data(self):
        proposal = sample()
        literal = 'quotes " and \' plus $(touch should-not-exist) `pwd`\nsecond line'
        proposal["tasks"][0]["title"] = literal
        proposal["tasks"][0]["description"] = literal
        argv = prepare(proposal)["commands"][0]["argv"]
        self.assertEqual(argv[argv.index("--title") + 1], literal)
        self.assertIn(literal, argv[argv.index("--description") + 1])

    def test_dependencies_use_topological_order_and_exact_existing_ids(self):
        proposal = split_proposal()
        existing = "wtp-deadbeef-0017"
        proposal["existingTaskIds"] = [existing]
        proposal["tasks"][0]["dependencies"] = [3, existing]
        proposal["tasks"][2]["dependencies"] = [2]
        result = prepare(proposal)
        self.assertEqual([row["number"] for row in result["commands"]], [2, 3, 1])
        self.assertEqual(result["commands"][2]["argv"][-2:], ["--depends-on", "@3," + existing])
        self.assertEqual(sum(row["count"] for row in result["distribution"]), 3)

    def test_invalid_graphs_and_missing_task_metadata_are_rejected(self):
        for deps, message in [([1], "itself"), ([99], "unknown proposal"),
                              (["wtp-9999"], "unknown existing"), ([True], "dependency must"),
                              ([2, 2], "duplicate dependency"), ([[2]], "dependency must")]:
            with self.subTest(deps=deps):
                proposal = split_proposal()
                proposal["tasks"][0]["dependencies"] = deps
                with self.assertRaisesRegex(ValueError, message):
                    prepare(proposal)
        proposal = split_proposal()
        proposal["tasks"][0]["dependencies"] = [2]
        proposal["tasks"][1]["dependencies"] = [1]
        with self.assertRaisesRegex(ValueError, "cycle"):
            prepare(proposal)
        for field in ("number", "title", "description", "acceptance", "verification", "estimate",
                      "priority", "lane", "dependencies", "model", "effort", "assignmentReason"):
            with self.subTest(missing=field):
                proposal = sample()
                del proposal["tasks"][0][field]
                with self.assertRaisesRegex(ValueError, "missing fields"):
                    prepare(proposal)
        for field, value in [("number", True), ("number", 0), ("estimate", "xxl"),
                             ("priority", "critical"), ("acceptance", ""), ("title", "bad\x00title")]:
            with self.subTest(field=field, value=value):
                proposal = sample()
                proposal["tasks"][0][field] = value
                with self.assertRaises(ValueError):
                    prepare(proposal)

    def test_gate_and_amended_revision(self):
        proposal = sample()
        digest = prepare(proposal)["digest"]
        for invalid in (None, "", "old-revision"):
            with self.subTest(digest=invalid), self.assertRaisesRegex(ValueError, "unchanged proposal"):
                prepare(proposal, "approve", invalid)
        for decision in ("amend", "refuse"):
            self.assertEqual(prepare(None, decision),
                             {"decision": decision, "handoffReady": False, "commands": []})
        with self.assertRaisesRegex(ValueError, "unknown decision"):
            prepare(proposal, "silence")
        mutations = [lambda p: p["tasks"][0].update(title="Amended title"),
                     lambda p: p["context"].update(store="/different/store"),
                     lambda p: p["plan"].update(scope="Expanded scope"),
                     lambda p: p.update(supportedMetadata=[])]
        for mutate in mutations:
            revised = copy.deepcopy(proposal)
            mutate(revised)
            with self.assertRaisesRegex(ValueError, "unchanged proposal"):
                prepare(revised, "approve", digest)

    def test_equivalent_key_order_keeps_commands_bound_to_digest(self):
        proposal = sample()
        proposal["tasks"][0]["metadata"] = {"project": "Example", "milestone": "MVP"}
        proposal["supportedMetadata"] = []
        preview = prepare(proposal)
        reordered = json.loads(json.dumps(proposal, sort_keys=True))
        approved = prepare(reordered, "approve", preview["digest"])
        self.assertEqual(preview["commands"], approved["commands"])

    def test_cli_rejects_malformed_or_duplicate_json(self):
        for payload in ('{"context":', '{"tasks": [], "tasks": []}'):
            result = subprocess.run([sys.executable, "-B", str(SCRIPT)], input=payload,
                                    text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, "")
            self.assertEqual(json.loads(result.stderr)["commands"], [])
        # The command-line decoder must not round a long invalid percentage.
        payload = json.dumps(split_proposal()).replace('"percent": 50',
                         '"percent": 50.00000000000000000000000000001', 1)
        result = subprocess.run([sys.executable, "-B", str(SCRIPT)], input=payload,
                                text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("sum to exactly 100", result.stderr)
        proposal = sample()
        proposal["tasks"][0]["title"] = 1.25
        result = subprocess.run([sys.executable, "-B", str(SCRIPT)], input=json.dumps(proposal),
                                text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("title must be non-empty text", result.stderr)

    def test_every_decision_runs_without_writes_or_process_launches(self):
        # Run the real entrypoint with an audit hook that rejects write/process
        # syscalls, even if a later helper change tries to invoke a WTP binary.
        harness = r'''
import os, runpy, sys
def guard(event, args):
    if event == "open":
        mode, flags = args[1], args[2]
        if (isinstance(mode, str) and any(c in mode for c in "wax+")) or (
                isinstance(flags, int) and flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC)):
            raise RuntimeError("write attempted")
    if event.startswith(("subprocess.", "socket.", "os.exec", "os.spawn")) or event in (
            "os.system", "os.mkdir", "os.remove", "os.rename", "os.rmdir", "os.chmod", "os.link", "os.symlink"):
        raise RuntimeError("mutation or process attempted: " + event)
sys.addaudithook(guard)
sys.argv = sys.argv[1:]
runpy.run_path(sys.argv[0], run_name="__main__")
'''
        proposal = sample()
        digest = prepare(proposal)["digest"]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # A legacy file would be migrated if storage-opening WTP calls ran.
            legacy = root / ".wtp" / "todo" / "legacy.json"
            legacy.parent.mkdir(parents=True)
            legacy.write_text("untouched", encoding="utf-8")
            before = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file()}
            for decision in ("preview", "amend", "refuse", "approve"):
                args = [sys.executable, "-B", "-c", harness, str(SCRIPT), "--decision", decision]
                if decision == "approve":
                    args += ["--approved-digest", digest]
                payload = "invalid draft" if decision in ("amend", "refuse") else json.dumps(proposal)
                result = subprocess.run(args, input=payload, cwd=root, text=True,
                                        capture_output=True, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                output = json.loads(result.stdout)
                self.assertEqual(output["handoffReady"], decision == "approve")
                after = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file()}
                self.assertEqual(before, after)


if __name__ == "__main__":
    unittest.main()
