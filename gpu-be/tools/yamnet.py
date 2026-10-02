#!/usr/bin/env python3
"""Audio-event detector (YAMNet, TFLite) used by gpu-be to hint the highlight LLM.

Usage: yamnet.py <audio-file>
Prints JSON to stdout:
  {"window": 0.975, "windows": [{"t": 0.0, "laugh": .., "applause": .., "shout": .., "gasp": .., "music": .., "silence": ..}, ...]}
Windows are 0.975 s long, 0.4875 s apart; each value is the max probability of the class group.
Audio is decoded by ffmpeg and streamed (O(1) memory).

Env: YAMNET_MODEL (default /opt/terex/yamnet.tflite), YAMNET_CLASSES (default /opt/terex/yamnet_class_map.csv)
"""
import csv
import json
import os
import subprocess
import sys

import numpy as np

try:
    from ai_edge_litert.interpreter import Interpreter
except ImportError:  # older runtime name
    from tflite_runtime.interpreter import Interpreter

MODEL = os.environ.get("YAMNET_MODEL", "/opt/terex/yamnet.tflite")
CLASSES = os.environ.get("YAMNET_CLASSES", "/opt/terex/yamnet_class_map.csv")
SR = 16000

# Exact YAMNet display names per group (exact match: "Music" must not pull in "Musical instrument").
GROUPS = {
    "laugh": {"Laughter", "Baby laughter", "Giggle", "Snicker", "Belly laugh", "Chuckle, chortle"},
    "applause": {"Applause", "Cheering"},
    "shout": {"Shout", "Bellow", "Whoop", "Yell", "Children shouting", "Screaming"},
    "gasp": {"Gasp"},
    "music": {"Music", "Singing", "Choir"},
    "silence": {"Silence"},
}


def class_indices():
    idx = {g: [] for g in GROUPS}
    with open(CLASSES, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            for group, names in GROUPS.items():
                if row["display_name"] in names:
                    idx[group].append(int(row["index"]))
    return idx


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: yamnet.py <audio-file>")
    idx = class_indices()

    interp = Interpreter(model_path=MODEL)
    interp.allocate_tensors()
    inp = interp.get_input_details()[0]
    n = int(np.prod(inp["shape"]))  # 15600 samples = 0.975 s
    hop = n // 2
    out = next(o for o in interp.get_output_details() if o["shape"][-1] == 521)  # class scores

    proc = subprocess.Popen(
        ["ffmpeg", "-v", "error", "-i", sys.argv[1], "-ac", "1", "-ar", str(SR), "-f", "s16le", "-"],
        stdout=subprocess.PIPE,
    )
    buf = np.zeros(0, dtype=np.float32)
    pos = 0  # absolute sample index of buf[0]
    windows = []

    def infer(chunk, t):
        interp.set_tensor(inp["index"], chunk.reshape(inp["shape"]).astype(np.float32))
        interp.invoke()
        scores = interp.get_tensor(out["index"]).reshape(-1, 521).mean(axis=0)
        w = {"t": round(t, 3)}
        for group, ids in idx.items():
            w[group] = float(scores[ids].max()) if ids else 0.0
        windows.append(w)

    while True:
        raw = proc.stdout.read(SR * 2 * 10)  # 10 s blocks (BufferedReader: full block or EOF)
        if not raw:
            break
        buf = np.concatenate([buf, np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0])
        while len(buf) >= n:
            infer(buf[:n], pos / SR)
            buf = buf[hop:]
            pos += hop
    if proc.wait() != 0:
        sys.exit("ffmpeg failed to decode the audio")

    json.dump({"window": n / SR, "windows": windows}, sys.stdout)


if __name__ == "__main__":
    main()
