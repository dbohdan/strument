"""Generate the probe image.

A five-digit number, large and unambiguous. The metric is whether the model
reports those exact digits, which is a count rather than a judgment -- "I see a
screenshot with a number" cannot pass, and neither can a near-miss.
"""
import random, sys, pathlib
from PIL import Image, ImageDraw, ImageFont


def font(size):
    for p in ("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
              "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
              "/usr/share/fonts/TTF/DejaVuSans.ttf"):
        if pathlib.Path(p).exists():
            return ImageFont.truetype(p, size)
    return ImageFont.load_default(size)


def make(path, digits, w=720, h=400):
    img = Image.new("RGB", (w, h), (250, 250, 248))
    d = ImageDraw.Draw(img)
    d.rectangle([8, 8, w - 9, h - 9], outline=(40, 40, 40), width=3)
    f = font(150)
    box = d.textbbox((0, 0), digits, font=f)
    d.text(((w - (box[2] - box[0])) / 2 - box[0], (h - (box[3] - box[1])) / 2 - box[1]),
           digits, fill=(10, 10, 10), font=f)
    small = font(28)
    d.text((26, h - 58), "verification code", fill=(90, 90, 90), font=small)
    img.save(path)
    return digits


if __name__ == "__main__":
    seed = int(sys.argv[2]) if len(sys.argv) > 2 else 20260914
    digits = str(random.Random(seed).randint(10000, 99999))
    print(make(sys.argv[1], digits))
