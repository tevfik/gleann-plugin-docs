#!/usr/bin/env python3
"""Regenerates testdata/page.png, the page used by the integration test.

A clean synthetic page: a title, a paragraph, a Turkish paragraph with every
special letter (ç ğ ı İ ö ş ü), a bulleted list and a ruled table. Needs Pillow
and the DejaVu fonts. Run: python3 testdata/gen_fixture.py
"""
from PIL import Image, ImageDraw, ImageFont

W, H = 1240, 1754  # A4 at 150 dpi
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf"
BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSerif-Bold.ttf"

img = Image.new("RGB", (W, H), "white")
d = ImageDraw.Draw(img)
title = ImageFont.truetype(BOLD, 54)
body = ImageFont.truetype(FONT, 30)

d.text((100, 120), "Quarterly Inventory Report", font=title, fill="black")

y = 260
for line in [
    "The warehouse received three shipments during the quarter.",
    "Total stock value increased by twelve percent compared with last year.",
    "Deliveries were on time in every region except the northern one.",
]:
    d.text((100, y), line, font=body, fill="black")
    y += 48

y += 40
for line in [
    "Çok güzel bir şehirde yaşıyoruz. Öğretmenler ığdır'dan geldi.",
    "İstanbul'da hava bugün güneşli ve rüzgârsız olacak.",
]:
    d.text((100, y), line, font=body, fill="black")
    y += 48

y += 40
for item in ["Inspect all incoming pallets", "Record serial numbers", "Report damaged goods"]:
    d.text((100, y), "• " + item, font=body, fill="black")
    y += 48

# Ruled table
y += 60
cols = [100, 480, 760, 1040]
rows = ["Item", "Quantity", "Price"], ["Bolt", "120", "0.35"], ["Washer", "300", "0.10"], ["Bracket", "45", "2.80"]
rh = 64
for r, row in enumerate(rows):
    top = y + r * rh
    for c, text in enumerate(row):
        d.text((cols[c] + 16, top + 14), text, font=body, fill="black")
for r in range(len(rows) + 1):
    d.line([(cols[0], y + r * rh), (cols[-1], y + r * rh)], fill="black", width=2)
for x in cols:
    d.line([(x, y), (x, y + len(rows) * rh)], fill="black", width=2)

img.save("testdata/page.png", optimize=True)
