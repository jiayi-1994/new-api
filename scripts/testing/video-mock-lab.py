"""Manage only the two local video scheduler mock fleets; preserve images and volumes."""

import argparse
import json
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
LABS = {
    "legacy": {
        "project": "codex-vsched-analysis-20261002",
        "compose": ".scratch/video-load-lab/compose.json",
        "services": [f"mock{i:02d}" for i in range(1, 21)]
        + [f"multi{i:02d}" for i in range(1, 21)]
        + ["inputmedia"],
    },
    "unified": {
        "project": "codex-unified-video-20261002",
        "compose": ".scratch/unified-video-model-plan/lab/compose.json",
        "services": [f"mock{i:02d}" for i in range(1, 21)],
    },
}


def docker(args: list[str], timeout: int = 30) -> str:
    result = subprocess.run(
        ["docker", *args], capture_output=True, text=True, encoding="utf-8",
        errors="replace", timeout=timeout, check=False,
    )
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or "Docker command failed")
    return result.stdout


def load_lab(name: str) -> dict:
    lab = dict(LABS[name])
    path = ROOT / lab["compose"]
    config = json.loads(path.read_text(encoding="utf-8-sig"))
    if config.get("name") != lab["project"]:
        raise ValueError(f"Refusing unexpected Compose project in {lab['compose']}")
    missing = set(lab["services"]) - config["services"].keys()
    if missing:
        raise ValueError(f"Missing mock services: {sorted(missing)}")
    lab["path"] = str(path)
    return lab


def containers(project: str) -> list[dict]:
    # Inspect selected fields only: never emit Compose environment variables or keys.
    output = docker([
        "ps", "--all", "--filter", f"label=com.docker.compose.project={project}",
        "--format", '{{.ID}}\t{{.Names}}\t{{.Label "com.docker.compose.service"}}\t{{.State}}',
    ])
    return [dict(zip(("id", "name", "service", "state"), line.split("\t")))
            for line in output.splitlines() if line]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("status", "start", "clean"), nargs="?", default="status")
    parser.add_argument("--lab", choices=("legacy", "unified", "both"), default="both")
    parser.add_argument("--services", nargs="+", help="Select named mock services within one lab")
    parser.add_argument("--apply", action="store_true", help="Execute start/clean; otherwise print the plan")
    args = parser.parse_args()
    if args.services and args.lab == "both":
        parser.error("--services requires --lab legacy or --lab unified")
    names = list(LABS) if args.lab == "both" else [args.lab]
    labs = [load_lab(name) for name in names]
    for lab in labs:
        if args.services:
            if set(args.services) - set(lab["services"]):
                parser.error("Only this lab's mock/multi/inputmedia services may be selected")
            lab["services"] = list(dict.fromkeys(args.services))

    if args.action == "status":
        for lab in labs:
            current = containers(lab["project"])
            print(json.dumps({"project": lab["project"], "mocks": [
                item for item in current if item["service"] in lab["services"]
            ], "preserved_services": [
                item for item in current if item["service"] not in lab["services"]
            ]}, ensure_ascii=False))
        return 0

    for lab in labs:
        print(json.dumps({"action": args.action, "project": lab["project"],
                          "services": lab["services"], "apply": args.apply,
                          "images_and_volumes": "preserved"}, ensure_ascii=False))
    if not args.apply:
        return 0

    # Resolve both projects before mutating either, so an unavailable engine fails closed.
    before = {lab["project"]: containers(lab["project"]) for lab in labs}
    for lab in labs:
        project = lab["project"]
        targets = [item for item in before[project] if item["service"] in lab["services"]]
        if args.action == "clean":
            for item in targets:
                # Check labels again immediately before stopping this exact container ID.
                labels = json.loads(docker([
                    "inspect", "--format", '{{json .Config.Labels}}', item["id"],
                ]))
                if (labels.get("com.docker.compose.project") != project
                        or labels.get("com.docker.compose.service") not in lab["services"]):
                    raise RuntimeError(f"Container ownership changed: {item['id']}")
                docker(["stop", "--time", "10", item["id"]])
                docker(["rm", item["id"]])  # No -v, no force, no image/network pruning.
        else:
            docker(["compose", "-p", project, "-f", lab["path"], "up", "-d",
                    "--no-deps", "--pull", "never", "--no-build", *lab["services"]], timeout=120)
        after = containers(project)
        if args.action == "clean" and any(item["service"] in lab["services"] for item in after):
            raise RuntimeError(f"Mock containers remain in {project}")
        if args.action == "start":
            running = {item["service"] for item in after if item["state"] == "running"}
            if set(lab["services"]) - running:
                raise RuntimeError(f"Some requested mock containers are not running in {project}")
        preserved_before = {(item["id"], item["state"]) for item in before[project]
                            if item["service"] not in lab["services"]}
        preserved_after = {(item["id"], item["state"]) for item in after
                           if item["service"] not in lab["services"]}
        if preserved_before != preserved_after:
            raise RuntimeError(f"Non-target container state changed in {project}; investigate before continuing")
        print(json.dumps({"project": project, "result": "verified", "action": args.action}))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"Not completed: {error}", file=sys.stderr)
        sys.exit(1)
