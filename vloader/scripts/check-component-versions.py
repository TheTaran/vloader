#!/usr/bin/env python3
"""Read-only upstream version check; issue management is explicitly opt-in."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request

TITLE = "Component updates available"
MARKER = "<!-- vloader-component-versions -->"


def version(value):
    match = re.fullmatch(r"v?(\d+)\.(\d+)(?:\.(\d+))?", value)
    if not match:
        raise ValueError(f"Unsupported stable version: {value}")
    return tuple(int(part or 0) for part in match.groups())


def fetch_json(url):
    request = urllib.request.Request(url, headers={"User-Agent": "vloader-version-check"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def pinned(root):
    dockerfile = (root / "Dockerfile").read_text()
    mod = (root / "go.mod").read_text()
    go = re.search(r"^go (\d+\.\d+\.\d+)$", mod, re.M)
    builder = re.search(r"^FROM golang:(\d+\.\d+\.\d+)-alpine", dockerfile, re.M)
    alpine = re.search(r"^FROM alpine:(\d+\.\d+(?:\.\d+)?)", dockerfile, re.M)
    if not all((go, builder, alpine)):
        raise ValueError("Cannot read Go and Alpine version pins")
    # Handles single-line require directives and require blocks, including indirects.
    modules = re.findall(r"^\s*(?:require\s+)?([\w./-]+)\s+(v\d+\.\d+\.\d+)\s*(?://.*)?$", mod, re.M)
    if not modules:
        raise ValueError("No pinned Go modules found")
    return go[1], builder[1], alpine[1], modules


def check(root, fetch=fetch_json):
    go, builder, alpine, modules = pinned(root)
    releases = fetch("https://go.dev/dl/?mode=json")
    latest_go = max((r["version"].removeprefix("go") for r in releases if r.get("stable")), key=version)
    branches = fetch("https://alpinelinux.org/releases.json")["release_branches"]
    stable = [b["rel_branch"].removeprefix("v") for b in branches if re.fullmatch(r"v\d+\.\d+", b["rel_branch"]) and b.get("releases")]
    latest_alpine = max(stable, key=version)
    # The current Alpine image is pinned to a minor branch; patch updates float.
    if len(alpine.split(".")) == 3:
        branch = next(b for b in branches if b["rel_branch"] == "v" + latest_alpine)
        latest_alpine = branch["releases"][0]["version"]
    rows = [("Go (go.mod)", go, latest_go), ("Go (Docker build)", builder, latest_go), ("Alpine runtime", alpine, latest_alpine)]
    for module, current in modules:
        escaped = "".join("!" + c.lower() if c.isupper() else c for c in module)
        latest = fetch(f"https://proxy.golang.org/{escaped}/@latest")["Version"]
        # Keep comparisons within the current module major path, excluding prereleases.
        version(latest)
        rows.append((module, current, latest))
    updates = any(version(latest) > version(current) for _, current, latest in rows)
    mismatch = go != builder
    lines = [MARKER, "Pinned build components and Go modules:", "", "| Component | Pinned | Latest stable | Status |", "| --- | --- | --- | --- |"]
    for name, current, latest in rows:
        status = "Update available" if version(latest) > version(current) else "Current or newer"
        lines.append(f"| {name} | `{current}` | `{latest}` | {status} |")
    if mismatch:
        lines.extend(["", "Go version mismatch: align go.mod and the Docker build stage."])
    lines.extend(["", "Review compatibility, update the relevant pins, run tests and rebuild. Release publication requires an explicitly chosen release tag; this check never changes versions or publishes images."])
    return updates or mismatch, "\n".join(lines) + "\n"


def manage_issue(updates, body):
    repo = os.environ["REPOSITORY"]
    def gh(*args):
        return subprocess.check_output(["gh", *args, "--repo", repo], text=True)
    # Match both title and marker so an unrelated issue is never modified.
    issues = json.loads(gh("issue", "list", "--state", "open", "--limit", "100", "--search", f'in:title "{TITLE}"', "--json", "number,title,body"))
    matching = [i for i in issues if i["title"] == TITLE and MARKER in (i["body"] or "")]
    if len(matching) > 1:
        raise ValueError("Multiple managed component issues exist; refusing ambiguous update")
    issue = str(matching[0]["number"]) if matching else None
    if not updates:
        if issue:
            gh("issue", "close", issue, "--reason", "completed")
        print("All pinned components are current.")
        return
    with tempfile.NamedTemporaryFile(mode="w", suffix=".md") as notes:
        notes.write(body)
        notes.flush()
        if issue:
            gh("issue", "edit", issue, "--title", TITLE, "--body-file", notes.name)
        else:
            gh("issue", "create", "--title", TITLE, "--body-file", notes.name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manage-issue", action="store_true")
    args = parser.parse_args()
    # Finish every upstream query before changing an existing issue.
    updates, body = check(Path(__file__).resolve().parents[1])
    print(body)
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as output:
            output.write(body)
    if args.manage_issue:
        manage_issue(updates, body)


if __name__ == "__main__":
    main()
