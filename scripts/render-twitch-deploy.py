"""Package the shared DB init script and pin the collector deployment image.

Uses only the standard library; it never reads credentials or contacts a cluster.
"""

import argparse
import json
from pathlib import Path
import re


ROOT = Path(__file__).resolve().parent.parent
IMAGE = "registry.legendary-trio.me/lt-inc/terex-twitch-collector"


def render(image: str, output: Path) -> None:
    if not re.fullmatch(re.escape(IMAGE) + r":[0-9a-f]{40}", image):
        raise ValueError("collector image must use the registry image and a full commit SHA")
    template = (ROOT / "deploy/k8s/twitch/collector.yaml").read_text(encoding="utf-8")
    placeholder = f"image: {IMAGE}:latest"
    if template.count(placeholder) != 1:
        raise ValueError("collector image placeholder must occur exactly once")
    configmap = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "terex-collector-db-init", "namespace": "admin"},
        "data": {"init.sh": (ROOT / "docker/collector-db-init.sh").read_text(encoding="utf-8")},
    }
    output.mkdir(parents=True, exist_ok=True)
    (output / "collector.yaml").write_text(
        template.replace(placeholder, f"image: {image}"), encoding="utf-8", newline="\n"
    )
    (output / "db-init-configmap.json").write_text(
        json.dumps(configmap, indent=2) + "\n", encoding="utf-8", newline="\n"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / ".twitch-deploy")
    args = parser.parse_args()
    try:
        render(args.image, args.output)
    except ValueError as error:
        parser.error(str(error))
