"""Generate the BridgeSafe narration track and deterministic caption timing.

    python scripts/generate-narration.py

Renders each block of narration-source.json to an mp3 with edge-tts, measures it
with ffprobe, and writes generated-narration.json with real durations and caption
cues. The Remotion composition reads only the generated file, so scene timing is
derived from the audio that actually exists rather than from an estimate.

It refuses to write output if two blocks would overlap, or if the last one runs
past the composition length. Getting that wrong is otherwise invisible until you
watch the render and hear two voices at once.
"""

from __future__ import annotations

import asyncio
import json
import re
import subprocess
import sys
from pathlib import Path

import edge_tts

# The Windows console defaults to cp1252, which cannot encode the characters used
# below (or in the narration itself). Force UTF-8 rather than degrading the text.
for stream in (sys.stdout, sys.stderr):
    if hasattr(stream, "reconfigure"):
        stream.reconfigure(encoding="utf-8")

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "src" / "narration-source.json"
OUTPUT = ROOT / "src" / "generated-narration.json"

VOICE = "en-GB-RyanNeural"
RATE = "+3%"
PITCH = "-1Hz"

# Must match DURATION_SECONDS in src/story.ts.
COMPOSITION_SECONDS = 190
# Minimum silence between the end of one block and the start of the next.
MIN_GAP = 0.2


def duration_seconds(path: Path) -> float:
    completed = subprocess.run(
        [
            "ffprobe", "-v", "error",
            "-show_entries", "format=duration",
            "-of", "default=noprint_wrappers=1:nokey=1",
            str(path),
        ],
        check=True, capture_output=True, text=True, encoding="utf-8",
    )
    return round(float(completed.stdout.strip()), 3)


def caption_weight(text: str) -> float:
    """Approximate how long a caption line takes to speak.

    Words dominate, but a full stop buys real silence, so punctuation is worth
    counting — without it the last line of a block drifts noticeably late.
    """
    words = len(re.findall(r"[\w’'-]+", text, flags=re.UNICODE))
    pauses = text.count(".") * 1.1 + text.count(",") * 0.3 + text.count(":") * 0.7 + text.count("—") * 0.4
    return max(1.0, words + pauses)


async def generate_block(item: dict) -> dict:
    target = ROOT / "public" / item["file"]
    target.parent.mkdir(parents=True, exist_ok=True)

    communicator = edge_tts.Communicate(item["script"], VOICE, rate=RATE, pitch=PITCH)
    await communicator.save(str(target))

    duration = duration_seconds(target)
    weights = [caption_weight(line) for line in item["captions"]]
    total = sum(weights)

    cursor = 0.0
    cues = []
    for index, weight in enumerate(weights):
        end = duration if index == len(weights) - 1 else round(cursor + duration * weight / total, 3)
        cues.append({"start": round(cursor, 3), "end": end})
        cursor = end

    return {**item, "duration": duration, "captionCues": cues, "voice": VOICE}


async def main() -> None:
    source = json.loads(SOURCE.read_text(encoding="utf-8"))
    generated = []

    for item in source:
        block = await generate_block(item)
        generated.append(block)
        end = block["start"] + block["duration"]
        print(f"  {block['id']:<10} {block['start']:>6.1f}s → {end:>6.1f}s  ({block['duration']:.2f}s)")

    problems = []
    for index, block in enumerate(generated[:-1]):
        nxt = generated[index + 1]
        end = block["start"] + block["duration"]
        if end + MIN_GAP > nxt["start"]:
            overlap = end + MIN_GAP - nxt["start"]
            problems.append(
                f"{block['id']} runs into {nxt['id']} by {overlap:.2f}s "
                f"(ends {end:.2f}s, next starts {nxt['start']:.2f}s)"
            )

    last = generated[-1]
    tail = last["start"] + last["duration"]
    if tail > COMPOSITION_SECONDS - 1.0:
        problems.append(
            f"{last['id']} ends at {tail:.2f}s, leaving under a second before the "
            f"{COMPOSITION_SECONDS}s composition ends"
        )

    if problems:
        print("\nNarration timing does not fit:", file=sys.stderr)
        for problem in problems:
            print(f"  - {problem}", file=sys.stderr)
        print("\nAdjust `start` values in narration-source.json, or shorten the script.", file=sys.stderr)
        raise SystemExit(1)

    OUTPUT.write_text(json.dumps(generated, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\n  narration ends at {tail:.1f}s of {COMPOSITION_SECONDS}s")
    print(f"  wrote {OUTPUT.relative_to(ROOT)}")


if __name__ == "__main__":
    asyncio.run(main())
