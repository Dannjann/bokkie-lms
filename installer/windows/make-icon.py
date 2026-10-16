"""Draws bokkie.ico: a blue tile with a white B, at every size Windows asks for.

Run with Pillow installed: python3 installer/windows/make-icon.py
The .ico and installer/macos/bokkie.icns are committed; this is how they were
made, kept so they can be made again. Then: iconutil -c icns installer/macos/bokkie.iconset
"""
import os
from PIL import Image, ImageDraw, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
FONT = "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
BLUE = (2, 112, 196, 255)  # primary.600 in web/src/theme.ts

def tile(size):
    scale = 4  # draw large and shrink, so the edges are smooth at 16px
    big = size * scale
    img = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle([0, 0, big - 1, big - 1], radius=big // 5, fill=BLUE)
    font = ImageFont.truetype(FONT, int(big * 0.72))
    box = d.textbbox((0, 0), "B", font=font)
    w, h = box[2] - box[0], box[3] - box[1]
    d.text(((big - w) / 2 - box[0], (big - h) / 2 - box[1]), "B", font=font, fill="white")
    return img.resize((size, size), Image.LANCZOS)

sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256]
tile(256).save(os.path.join(HERE, "bokkie.ico"), sizes=[(s, s) for s in sizes],
               append_images=[tile(s) for s in sizes])


# macOS: the same tile inside the transparent margin the Mac icon grid expects
# (824 of 1024), as an .iconset that iconutil turns into bokkie.icns.
MAC = os.path.join(HERE, "..", "macos")
iconset = os.path.join(MAC, "bokkie.iconset")
os.makedirs(iconset, exist_ok=True)
def mac_tile(size):
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    inner = round(size * 824 / 1024)
    img.paste(tile(inner), ((size - inner) // 2, (size - inner) // 2))
    return img
for base in [16, 32, 128, 256, 512]:
    mac_tile(base).save(os.path.join(iconset, "icon_%dx%d.png" % (base, base)))
    mac_tile(base * 2).save(os.path.join(iconset, "icon_%dx%d@2x.png" % (base, base)))
