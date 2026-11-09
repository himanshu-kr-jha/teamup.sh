#!/usr/bin/env python3
"""Draws the call backgrounds in public/backgrounds/: soft light falling on a
coloured wall, so a person in front of one looks like they are in a room rather
than pasted on a poster. Drawn rather than photographed, so they belong to the
project outright. Run from web/: python3 scripts/make-backgrounds.py"""

import os

import numpy as np
from PIL import Image

W, H = 1280, 720
OUT = os.path.join(os.path.dirname(__file__), "..", "public", "backgrounds")

# name: (base colour, [(x, y, radius, colour, strength)], vignette)
SCENES = {
    "studio": ((206, 203, 198), [(0.30, 0.25, 0.75, (238, 236, 232), 0.9), (0.85, 0.9, 0.6, (176, 172, 166), 0.5)], 0.18),
    "indigo": ((52, 48, 120), [(0.25, 0.2, 0.7, (99, 102, 241), 0.8), (0.85, 0.75, 0.6, (139, 92, 246), 0.6), (0.6, 0.05, 0.4, (165, 180, 252), 0.35)], 0.25),
    "dawn": ((236, 196, 178), [(0.2, 0.15, 0.7, (255, 228, 205), 0.9), (0.85, 0.8, 0.65, (229, 152, 155), 0.6), (0.7, 0.2, 0.4, (250, 208, 170), 0.4)], 0.15),
    "ocean": ((24, 74, 96), [(0.3, 0.25, 0.75, (45, 140, 160), 0.8), (0.85, 0.8, 0.55, (20, 50, 90), 0.6), (0.7, 0.1, 0.4, (110, 190, 200), 0.3)], 0.25),
    "forest": ((38, 66, 48), [(0.25, 0.2, 0.7, (84, 122, 82), 0.8), (0.8, 0.85, 0.6, (24, 44, 34), 0.6), (0.65, 0.15, 0.35, (150, 170, 110), 0.3)], 0.28),
    "night": ((22, 26, 38), [(0.3, 0.2, 0.7, (52, 60, 92), 0.8), (0.85, 0.75, 0.5, (70, 50, 100), 0.45)], 0.35),
}


def draw(base, lights, vignette):
    y, x = np.mgrid[0:H, 0:W].astype(np.float32)
    x /= W
    y /= H
    aspect = W / H
    img = np.ones((H, W, 3), np.float32) * np.array(base, np.float32)
    for lx, ly, radius, colour, strength in lights:
        d = np.sqrt(((x - lx) * aspect) ** 2 + (y - ly) ** 2) / (radius * aspect)
        weight = (np.clip(1 - d, 0, 1) ** 2 * strength)[..., None]
        img = img * (1 - weight) + np.array(colour, np.float32) * weight
    # Darker towards the corners, as a lens sees a room.
    d = np.sqrt(((x - 0.5) * 1.2) ** 2 + (y - 0.5) ** 2)
    img *= (1 - vignette * np.clip(d / 0.75, 0, 1) ** 2)[..., None]
    # A little grain, so the gradient does not band once the call compresses it.
    rng = np.random.default_rng(7)
    img += rng.normal(0, 1.6, img.shape)
    return Image.fromarray(np.clip(img, 0, 255).astype(np.uint8))


os.makedirs(OUT, exist_ok=True)
for name, (base, lights, vignette) in SCENES.items():
    picture = draw(base, lights, vignette)
    picture.save(os.path.join(OUT, f"{name}.jpg"), quality=86, optimize=True, progressive=True)
    picture.resize((160, 90), Image.LANCZOS).save(os.path.join(OUT, f"{name}-thumb.jpg"), quality=82, optimize=True)
    print(name)
