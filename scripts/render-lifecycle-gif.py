#!/usr/bin/env python3
"""Render a short terminal GIF from a lifecycle transcript JSON document."""

from __future__ import annotations

import json
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

WIDTH = 820
HEIGHT = 460
MARGIN_X = 20
TITLE_H = 34
LINE_H = 20
FONT_SIZE = 14
TYPE_CHUNK = 10
TYPE_MS = 42
HOLD_MS = 420
END_MS = 2200
PROMPT = "$ "

BG = (18, 22, 28)
BAR = (28, 33, 41)
FG = (226, 232, 240)
DIM = (132, 142, 153)
PROMPT_FG = (125, 196, 140)
OK_FG = (110, 186, 128)
DOT_RED = (232, 95, 92)
DOT_YELLOW = (232, 176, 62)
DOT_GREEN = (88, 180, 96)


def load_font() -> ImageFont.FreeTypeFont:
    for path in (
        "/System/Library/Fonts/SFNSMono.ttf",
        "/System/Library/Fonts/Menlo.ttc",
        "/System/Library/Fonts/Monaco.ttf",
    ):
        try:
            return ImageFont.truetype(path, FONT_SIZE)
        except OSError:
            continue
    return ImageFont.load_default()


def wrap(text: str, font: ImageFont.ImageFont, max_width: int) -> list[str]:
    if not text:
        return [""]
    words = text.split(" ")
    lines: list[str] = []
    current = words[0]
    for word in words[1:]:
        trial = f"{current} {word}"
        if font.getlength(trial) <= max_width:
            current = trial
        else:
            lines.append(current)
            current = word
    lines.append(current)
    return lines


def new_frame(font: ImageFont.ImageFont) -> tuple[Image.Image, ImageDraw.ImageDraw]:
    img = Image.new("RGB", (WIDTH, HEIGHT), BG)
    draw = ImageDraw.Draw(img)
    draw.rectangle((0, 0, WIDTH, TITLE_H), fill=BAR)
    for x, color in ((18, DOT_RED), (38, DOT_YELLOW), (58, DOT_GREEN)):
        draw.ellipse((x, 12, x + 12, 24), fill=color)
    title = "provehito"
    tw = font.getlength(title)
    draw.text(((WIDTH - tw) / 2, 9), title, font=font, fill=DIM)
    return img, draw


def blit_lines(
    draw: ImageDraw.ImageDraw,
    font: ImageFont.ImageFont,
    lines: list[str],
    typed_prefix: str | None,
) -> None:
    y = TITLE_H + 14
    max_y = HEIGHT - 16
    max_width = WIDTH - (MARGIN_X * 2)
    visible: list[str] = []
    for line in lines:
        visible.extend(wrap(line, font, max_width))
    if typed_prefix is not None:
        visible.extend(wrap(typed_prefix, font, max_width))
    max_rows = max((max_y - y) // LINE_H, 1)
    if len(visible) > max_rows:
        visible = visible[-max_rows:]
    for line in visible:
        if y + LINE_H > max_y:
            break
        if line.startswith(PROMPT):
            draw.text((MARGIN_X, y), PROMPT, font=font, fill=PROMPT_FG)
            rest = line[len(PROMPT) :]
            draw.text((MARGIN_X + font.getlength(PROMPT), y), rest, font=font, fill=FG)
        elif line.startswith("#"):
            draw.text((MARGIN_X, y), line, font=font, fill=DIM)
        elif line.startswith("RESULT: OK"):
            draw.text((MARGIN_X, y), line, font=font, fill=OK_FG)
        else:
            # Continuation of a wrapped command.
            draw.text((MARGIN_X, y), line, font=font, fill=FG)
        y += LINE_H


def add_frame(
    frames: list[Image.Image],
    durations: list[int],
    font: ImageFont.ImageFont,
    lines: list[str],
    duration_ms: int,
    typed_prefix: str | None = None,
) -> None:
    img, draw = new_frame(font)
    blit_lines(draw, font, lines, typed_prefix)
    frames.append(img)
    durations.append(duration_ms)


def render(steps: list[dict], output: Path) -> None:
    font = load_font()
    max_width = WIDTH - (MARGIN_X * 2)
    frames: list[Image.Image] = []
    durations: list[int] = []
    committed: list[str] = []

    add_frame(frames, durations, font, committed, 280)

    for step in steps:
        comment = f"# {step['label']}"
        add_frame(frames, durations, font, committed + [comment], 260)
        committed.append(comment)

        command = f"{PROMPT}{step['command']}"
        for i in range(TYPE_CHUNK, len(command) + TYPE_CHUNK, TYPE_CHUNK):
            typed = command[: min(i, len(command))]
            hold = TYPE_MS if i < len(command) else HOLD_MS // 3
            add_frame(frames, durations, font, committed, hold, typed)
        committed.append(command)

        output_lines = [
            line.rstrip("\n")
            for line in step["output"].splitlines()
            if line and line != "Evidence: completed"
        ]
        for line in output_lines:
            committed.extend(wrap(line, font, max_width))
        add_frame(frames, durations, font, committed, HOLD_MS)

    if frames:
        durations[-1] = END_MS

    anchor = frames[-1].quantize(colors=24, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    quantized = [frame.quantize(palette=anchor, dither=Image.Dither.NONE) for frame in frames]
    output.parent.mkdir(parents=True, exist_ok=True)
    quantized[0].save(
        output,
        save_all=True,
        append_images=quantized[1:],
        duration=durations,
        loop=0,
        optimize=True,
        disposal=2,
    )


def main() -> int:
    payload = json.load(sys.stdin)
    render(payload["steps"], Path(payload["output"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
