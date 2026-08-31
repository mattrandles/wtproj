#!/usr/bin/env python3
"""Validate and preview a WTP proposal on stdin. Never execute or write it."""

import argparse
import hashlib
import json
import sys
from collections import Counter
from decimal import Decimal, InvalidOperation
from fractions import Fraction


METADATA_FLAGS = {
    "lane": "--lane",
    "issueId": "--issue-id",
    "project": "--project",
    "milestone": "--milestone",
    "version": "--version",
    "featureId": "--feature-id",
    "feature": "--feature",
    "gitRepo": "--git-repo",
    "gitBranch": "--git-branch",
    "worktreeName": "--worktree-name",
    "worktreeDir": "--worktree-dir",
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def fields(value, required, optional=()):
    require(isinstance(value, dict), "expected an object")
    require(set(required) <= value.keys(), "missing fields: " + ", ".join(sorted(set(required) - value.keys())))
    require(value.keys() <= set(required) | set(optional), "unknown fields: " + ", ".join(sorted(value.keys() - set(required) - set(optional))))


def text(value, label):
    require(isinstance(value, str) and value.strip() and "\x00" not in value,
            label + " must be non-empty text without NUL")
    return value


def unique_strings(value, label):
    require(isinstance(value, list), label + " must be a list")
    for item in value:
        text(item, label)
    require(len(set(value)) == len(value), label + " contains duplicates")
    return value


def pair(value):
    return (text(value.get("model"), "model"), text(value.get("effort"), "effort"))


def distribution_quotas(inventory, distribution, count):
    require(isinstance(inventory, dict) and inventory, "enabledModels must be a non-empty inventory")
    for model, efforts in inventory.items():
        text(model, "enabled model")
        require(unique_strings(efforts, "efforts"), "each enabled model needs supported efforts")
    require(isinstance(distribution, list) and distribution, "distribution must be non-empty")
    shares = {}
    for entry in distribution:
        fields(entry, ("model", "effort", "percent"))
        key = pair(entry)
        require(key not in shares, "duplicate model/effort in distribution")
        require(key[0] in inventory and key[1] in inventory[key[0]], "unsupported model/effort: " + " ".join(key))
        require(type(entry["percent"]) in (int, float, str, Decimal), "percent must be numeric")
        try:
            percent = Decimal(str(entry["percent"]))
        except InvalidOperation:
            raise ValueError("percent must be numeric") from None
        require(percent.is_finite() and 0 <= percent <= 100, "percent must be finite and between 0 and 100")
        # Decimal arithmetic uses a precision context; exact rationals keep
        # even long decimal inputs from rounding an invalid total to 100.
        shares[key] = Fraction(percent)
    require(sum(shares.values()) == 100, "percentages must sum to exactly 100; ask for a corrected split")
    raw = {key: count * share / 100 for key, share in shares.items()}
    quotas = {key: int(value) for key, value in raw.items()}
    # Stable sort gives ties to the user's distribution order.
    order = sorted(raw, key=lambda key: raw[key] - quotas[key], reverse=True)
    for key in order[:count - sum(quotas.values())]:
        quotas[key] += 1
    return shares, quotas


def validate(proposal):
    fields(proposal, ("context", "plan", "enabledModels", "distribution",
                      "existingTaskIds", "supportedMetadata", "tasks"),
           ("distributionDeviation",))
    fields(proposal["context"], ("root", "branch", "store"))
    fields(proposal["plan"], ("scope", "approach", "assumptions", "dependencies",
                              "risks", "acceptance", "verification"))
    for section in (proposal["context"], proposal["plan"]):
        for key, value in section.items():
            text(value, key)
    existing = unique_strings(proposal["existingTaskIds"], "existingTaskIds")
    require(all(not any(c in item for c in ",@\r\n") for item in existing), "existing IDs cannot contain dependency delimiters")
    supported = unique_strings(proposal["supportedMetadata"], "supportedMetadata")
    require(set(supported) <= METADATA_FLAGS.keys(), "unknown supported metadata field")
    tasks = proposal["tasks"]
    require(isinstance(tasks, list) and tasks, "tasks must be non-empty")
    shares, quotas = distribution_quotas(proposal["enabledModels"], proposal["distribution"], len(tasks))
    by_number = {}
    counts = Counter()
    for task in tasks:
        fields(task, ("number", "title", "description", "acceptance", "verification",
                      "priority", "estimate", "lane", "dependencies", "model", "effort",
                      "assignmentReason"), ("metadata",))
        number = task["number"]
        require(type(number) is int and number > 0, "task number must be a positive integer")
        require(number not in by_number, "duplicate task number")
        by_number[number] = task
        for key in ("title", "description", "acceptance", "verification", "lane", "assignmentReason"):
            text(task[key], key)
        require(task["priority"] in ("low", "medium", "high", "urgent"), "invalid priority")
        require(task["estimate"] in ("xs", "s", "m", "l", "xl"), "invalid estimate")
        key = pair(task)
        require(key in shares and shares[key] > 0, "task assigned to an unselected or zero-share model/effort")
        counts[key] += 1
        metadata = task.get("metadata", {})
        fields(metadata, (), set(METADATA_FLAGS) - {"lane"})
        for name, value in metadata.items():
            text(value, name)
        deps = task["dependencies"]
        require(isinstance(deps, list), "dependencies must be a list")
        for dep in deps:
            require(type(dep) in (int, str), "dependency must be a proposal number or existing ID")
            require(dep != number, "task cannot depend on itself")
            if isinstance(dep, str):
                require(dep in existing, "unknown existing dependency: " + dep)
        require(len(set(deps)) == len(deps), "duplicate dependency")
    for task in tasks:
        for dep in task["dependencies"]:
            require(not isinstance(dep, int) or dep in by_number, "unknown proposal dependency")
    ordered = []
    pending = dict(by_number)
    while pending:
        ready = [number for number, task in pending.items()
                 if not any(dep in pending for dep in task["dependencies"] if isinstance(dep, int))]
        require(ready, "dependency cycle")
        for number in ready:
            ordered.append(pending.pop(number))
    if any(counts[key] != quotas[key] for key in shares):
        text(proposal.get("distributionDeviation"), "distributionDeviation (complexity/risk justification)")
    elif "distributionDeviation" in proposal:
        text(proposal["distributionDeviation"], "distributionDeviation")
    return ordered, shares, quotas, counts


def command(task, supported, scope):
    description = "\n\n".join((
        "Scope: " + scope,
        task["description"],
        "Acceptance: " + task["acceptance"],
        "Verification: " + task["verification"],
        "Model: " + task["model"] + "; reasoning effort: " + task["effort"],
        "Assignment reason: " + task["assignmentReason"],
    ))
    metadata_args, warnings = [], []
    for name, value in sorted({"lane": task["lane"], **task.get("metadata", {})}.items()):
        if name in supported:
            metadata_args.extend((METADATA_FLAGS[name], value))
        else:
            description += "\n\n" + name + ": " + value
            warnings.append("Task %s: unsupported %s preserved in description" % (task["number"], name))
    argv = ["wtp", "--json", "task", "create", "--title", task["title"],
            "--description", description, "--status", "todo", "--priority", task["priority"],
            "--estimate", task["estimate"], "--model", " ".join(pair(task))] + metadata_args
    if task["dependencies"]:
        argv.extend(("--depends-on", ",".join("@" + str(dep) if isinstance(dep, int) else dep
                                             for dep in task["dependencies"])))
    return {"number": task["number"], "argv": argv}, warnings


def prepare(proposal, decision="preview", approved_digest=None):
    require(decision in ("preview", "amend", "refuse", "approve"), "unknown decision")
    # Cancellation or amendment must work even for an incomplete/invalid draft.
    if decision in ("amend", "refuse"):
        return {"decision": decision, "handoffReady": False, "commands": []}
    ordered, shares, quotas, counts = validate(proposal)
    canonical = json.dumps(proposal, sort_keys=True, ensure_ascii=True, separators=(",", ":"), default=str)
    digest = hashlib.sha256(canonical.encode()).hexdigest()
    require(decision != "approve" or approved_digest == digest,
            "approval must reference the unchanged proposal digest")
    commands, warnings = [], []
    for task in ordered:
        argv, task_warnings = command(task, proposal["supportedMetadata"], proposal["plan"]["scope"])
        commands.append(argv)
        warnings.extend(task_warnings)
    summary = []
    for key, share in shares.items():
        actual = Fraction(counts[key] * 100, len(ordered))
        summary.append({"model": key[0], "effort": key[1], "requestedPercent": float(share),
                        "quota": quotas[key], "count": counts[key], "actualPercent": float(actual)})
        if quotas[key] * 100 != share * len(ordered):
            warnings.append("Integer rounding changes the target percentage for " + " ".join(key))
        if counts[key] != quotas[key]:
            warnings.append("Quota deviation for " + " ".join(key) + ": " + proposal["distributionDeviation"])
    return {"decision": decision, "handoffReady": decision == "approve", "digest": digest,
            "context": proposal["context"], "distribution": summary, "warnings": warnings,
            "commands": commands, "execution": "preview only; no WTP commands executed"}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field: " + key)
        result[key] = value
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--decision", choices=("preview", "amend", "refuse", "approve"), default="preview")
    parser.add_argument("--approved-digest")
    args = parser.parse_args()
    try:
        # Preserve precision without turning numeric text fields into strings.
        proposal = None if args.decision in ("amend", "refuse") else json.load(
            sys.stdin, parse_float=Decimal, object_pairs_hook=unique_object)
        result = prepare(proposal, args.decision, args.approved_digest)
    except (ValueError, TypeError, OverflowError) as error:
        print(json.dumps({"error": str(error), "handoffReady": False, "commands": []}), file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2, ensure_ascii=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
