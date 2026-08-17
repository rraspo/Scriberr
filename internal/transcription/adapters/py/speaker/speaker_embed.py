#!/usr/bin/env python3
"""Extract one speaker embedding per diarized speaker.

Reads an audio file plus its diarization segments and emits a single embedding
vector per speaker, computed from that speaker's longest segments rather than
the whole recording. A few seconds of clean speech identifies a voice as well
as an hour of it and costs a fraction as much.

Top-level imports are stdlib only on purpose: the segment-selection logic is
pure and must stay importable, and therefore testable, without torch, pyannote
or a warm model cache. Everything heavy is imported inside the function that
needs it.
"""
import argparse
import json
import os
import sys
from collections import defaultdict

DEFAULT_MAX_SEGMENTS = 3
DEFAULT_MAX_SECONDS = 30.0
DEFAULT_MIN_SECONDS = 2.0
EMBEDDING_MODEL = "pyannote/wespeaker-voxceleb-resnet34-LM"


def select_segments(
    segments,
    max_segments=DEFAULT_MAX_SEGMENTS,
    max_seconds=DEFAULT_MAX_SECONDS,
    min_seconds=DEFAULT_MIN_SECONDS,
):
    """Pick the segments to embed for each speaker.

    Returns {speaker: {"segments": [(start, end)], "seconds": float,
    "usable": bool}}. A speaker whose selected audio falls short of
    min_seconds is marked unusable: a vector built from a fragment is worse
    than no vector, because it looks like an answer.
    """
    by_speaker = defaultdict(list)
    for segment in segments:
        speaker = segment.get("speaker")
        if not speaker:
            continue
        try:
            start = float(segment["start"])
            end = float(segment["end"])
        except (KeyError, TypeError, ValueError):
            continue
        if end - start <= 0:
            continue
        by_speaker[speaker].append((start, end))

    selection = {}
    for speaker, spans in by_speaker.items():
        spans.sort(key=lambda span: span[1] - span[0], reverse=True)
        chosen = []
        total = 0.0
        for start, end in spans:
            if len(chosen) >= max_segments:
                break
            chosen.append((start, end))
            total += end - start
            if total >= max_seconds:
                break
        chosen.sort()
        selection[speaker] = {
            "segments": chosen,
            "seconds": total,
            "usable": total >= min_seconds,
        }
    return selection


def _load_inference(device, hf_token):
    """Build the embedding inference. Heavy imports live here by design."""
    from pyannote.audio import Inference, Model

    model = Model.from_pretrained(EMBEDDING_MODEL, use_auth_token=hf_token)
    inference = Inference(model, window="whole")
    if device:
        import torch

        inference.to(torch.device(device))
    return inference


def embed_speakers(audio_path, selection, device, hf_token):
    """Embed each usable speaker; unusable ones come back as None."""
    import numpy
    from pyannote.core import Segment

    results = {}
    inference = None
    for speaker, chosen in selection.items():
        if not chosen["usable"]:
            results[speaker] = None
            continue
        if inference is None:
            inference = _load_inference(device, hf_token)
        vectors = [
            numpy.asarray(inference.crop(audio_path, Segment(start, end))).reshape(-1)
            for start, end in chosen["segments"]
        ]
        averaged = numpy.mean(numpy.vstack(vectors), axis=0)
        norm = numpy.linalg.norm(averaged)
        # A zero-norm vector carries no direction, so it cannot be compared.
        results[speaker] = (averaged / norm) if norm > 0 else None
    return results


def main(argv=None):
    parser = argparse.ArgumentParser(prog="speaker_embed.py")
    parser.add_argument("--audio", required=True, help="Path to the audio file")
    parser.add_argument("--segments", required=True, help="Path to diarization segments JSON")
    parser.add_argument("--output", required=True, help="Path to write the embeddings JSON")
    parser.add_argument("--max-segments", type=int, default=DEFAULT_MAX_SEGMENTS)
    parser.add_argument("--max-seconds", type=float, default=DEFAULT_MAX_SECONDS)
    parser.add_argument("--min-seconds", type=float, default=DEFAULT_MIN_SECONDS)
    parser.add_argument("--device", default="cpu")
    parser.add_argument(
        "--hf-token",
        default=None,
        help="HuggingFace token; falls back to the HF_TOKEN environment variable",
    )
    args = parser.parse_args(argv)

    with open(args.segments, "r", encoding="utf-8") as handle:
        segments = json.load(handle)

    selection = select_segments(
        segments,
        max_segments=args.max_segments,
        max_seconds=args.max_seconds,
        min_seconds=args.min_seconds,
    )

    hf_token = args.hf_token or os.environ.get("HF_TOKEN")
    embeddings = embed_speakers(args.audio, selection, args.device, hf_token)

    dimensions = 0
    speakers = []
    for speaker in sorted(selection):
        vector = embeddings.get(speaker)
        if vector is not None:
            dimensions = len(vector)
        speakers.append(
            {
                "speaker": speaker,
                "embedding": [float(value) for value in vector] if vector is not None else None,
                "seconds_used": round(selection[speaker]["seconds"], 3),
                "segments_used": len(selection[speaker]["segments"]),
            }
        )

    with open(args.output, "w", encoding="utf-8") as handle:
        json.dump({"dimensions": dimensions, "speakers": speakers}, handle)

    # Vectors and paths stay out of stdout; only the output file carries them.
    print(f"embedded {sum(1 for s in speakers if s['embedding'] is not None)}/{len(speakers)} speakers")
    return 0


if __name__ == "__main__":
    sys.exit(main())
