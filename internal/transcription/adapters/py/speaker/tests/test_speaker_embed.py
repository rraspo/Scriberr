"""Tests for speaker_embed.py segment selection.

These exercise pure functions only: importing speaker_embed must not pull in
torch or pyannote, so this file runs under a bare pytest with no bootstrapped
environment and no model cache.
"""
import sys
from pathlib import Path

import pytest

SCRIPT_DIR = Path(__file__).parent.parent
sys.path.insert(0, str(SCRIPT_DIR))

from speaker_embed import select_segments  # noqa: E402


def seg(speaker, start, end):
    return {"speaker": speaker, "start": start, "end": end}


def test_selects_the_longest_segments_up_to_max_segments():
    segments = [
        seg("SPEAKER_00", 0, 1),
        seg("SPEAKER_00", 10, 19),
        seg("SPEAKER_00", 20, 22),
        seg("SPEAKER_00", 30, 38),
        seg("SPEAKER_00", 40, 43),
        seg("SPEAKER_00", 50, 57),
        seg("SPEAKER_00", 60, 61),
        seg("SPEAKER_00", 70, 74),
    ]

    selection = select_segments(segments, max_segments=3, max_seconds=300.0)

    chosen = selection["SPEAKER_00"]["segments"]
    assert len(chosen) == 3
    durations = sorted(end - start for start, end in chosen)
    assert durations == [7.0, 8.0, 9.0]


def test_stops_early_once_max_seconds_is_reached():
    segments = [
        seg("SPEAKER_00", 0, 20),
        seg("SPEAKER_00", 30, 45),
        seg("SPEAKER_00", 50, 60),
    ]

    selection = select_segments(segments, max_segments=3, max_seconds=30.0)

    chosen = selection["SPEAKER_00"]["segments"]
    # The 20s segment alone is under the cap; adding the 15s one reaches it.
    assert len(chosen) == 2
    assert selection["SPEAKER_00"]["seconds"] == pytest.approx(35.0)


def test_groups_speakers_independently():
    segments = [
        seg("SPEAKER_00", 0, 9),
        seg("SPEAKER_01", 10, 14),
        seg("SPEAKER_00", 20, 23),
        seg("SPEAKER_01", 30, 38),
    ]

    selection = select_segments(segments, max_segments=1, max_seconds=300.0)

    assert set(selection) == {"SPEAKER_00", "SPEAKER_01"}
    assert selection["SPEAKER_00"]["segments"] == [(0.0, 9.0)]
    assert selection["SPEAKER_01"]["segments"] == [(30.0, 38.0)]


def test_speaker_below_the_audio_floor_is_marked_unusable():
    segments = [
        seg("SPEAKER_00", 0, 9),
        seg("SPEAKER_01", 10, 11.5),
    ]

    selection = select_segments(segments, max_segments=3, max_seconds=300.0, min_seconds=2.0)

    assert selection["SPEAKER_00"]["usable"] is True
    # Under the floor: a garbage vector is worse than admitting we cannot tell.
    assert selection["SPEAKER_01"]["usable"] is False


def test_zero_length_and_inverted_segments_are_discarded():
    segments = [
        seg("SPEAKER_00", 5, 5),
        seg("SPEAKER_00", 9, 4),
        seg("SPEAKER_00", 10, 20),
    ]

    selection = select_segments(segments, max_segments=3, max_seconds=300.0)

    assert selection["SPEAKER_00"]["segments"] == [(10.0, 20.0)]


def test_segments_without_a_speaker_label_are_ignored():
    segments = [
        seg("SPEAKER_00", 0, 9),
        {"start": 10, "end": 20},
        {"speaker": None, "start": 30, "end": 40},
    ]

    selection = select_segments(segments, max_segments=3, max_seconds=300.0)

    assert set(selection) == {"SPEAKER_00"}


def test_empty_input_yields_no_speakers():
    assert select_segments([], max_segments=3, max_seconds=30.0) == {}
