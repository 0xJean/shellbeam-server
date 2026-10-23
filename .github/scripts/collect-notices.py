#!/usr/bin/env python3
"""Bundle license texts from the exact Go module versions used by this checkout."""

import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess


def modules():
    # `go list -m` emits concatenated JSON objects, not one JSON array.
    raw = subprocess.check_output(["go", "list", "-m", "-json", "all"], text=True)
    decoder = json.JSONDecoder()
    while raw.strip():
        raw = raw.lstrip()
        module, end = decoder.raw_decode(raw)
        raw = raw[end:]
        if not module.get("Main"):
            yield module


def license_files(directory):
    pattern = re.compile(r"^(unlicense|licen[cs]e|copying|copyright|notice|patents)([._-].*)?$", re.I)
    return sorted(p for p in directory.rglob("*") if p.is_file() and pattern.match(p.name))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if args.output.exists():
        raise SystemExit("Output already exists; use a fresh build directory")
    args.output.mkdir(parents=True)
    inventory = []
    for module in modules():
        source = module.get("Replace", module)
        directory = Path(source.get("Dir", ""))
        if not source.get("Dir") or not directory.is_dir():
            raise SystemExit(f"Missing module source: {module['Path']}; run go mod download all")
        texts = license_files(directory)
        if not texts:
            raise SystemExit(f"Missing license text: {module['Path']}")
        name = module["Path"] + "@" + module.get("Version", "local")
        # Go deliberately skips directories beginning with '_'. Module paths
        # can contain @versions or major-version subdirectories and must not
        # be traversed by a later `go test ./...` or `go vet ./...`.
        destination = args.output / "_modules" / name
        for text in texts:
            target = destination / text.relative_to(directory)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(text, target)
        inventory.append({"module": module["Path"], "version": module.get("Version"),
                          "license_files": [str(p.relative_to(directory)) for p in texts]})
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    for name in ["LICENSE", "PATENTS"]:
        if (goroot / name).is_file():
            target = args.output / "go-toolchain" / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(goroot / name, target)
    (args.output / "modules.json").write_text(json.dumps(inventory, indent=2) + "\n")
    (args.output / "README.txt").write_text(
        "Third-party license and attribution texts for ShellBeam Server.\n"
        "Generated from the selected Go module graph and Go toolchain.\n"
        "modules.json records module versions and copied license files in _modules/.\n"
        "This inventory includes transitive and test-only modules for completeness.\n"
        "The server's own LICENSE and upstream NOTICE are supplied separately.\n"
    )
    print(f"Collected third-party license texts for {len(inventory)} Go modules")


if __name__ == "__main__":
    main()

