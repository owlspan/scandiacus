# Writes the PNG fixtures for tests/png.scd: every colour type, the palette
# depths, each row filter, stored and compressed data, and the IDAT split
# over two chunks. It prints the cases with the hash of the premultiplied
# BGRA pixels the decoder has to produce. Needs Pillow for pillow.png.

import zlib, struct, hashlib, random
from PIL import Image

random.seed(7)
W, H = 37, 23   # odd sizes, so nothing lines up by luck

def expected(rgba, w, h):
    out = bytearray(struct.pack('<II', w, h))
    for (r, g, b, a) in rgba:
        out += bytes([(b*a+127)//255, (g*a+127)//255, (r*a+127)//255, a])
    return hashlib.sha256(bytes(out)).hexdigest()

def chunk(tag, data):
    return struct.pack('>I', len(data)) + tag + data + struct.pack('>I', zlib.crc32(tag+data) & 0xffffffff)

def raw_png(path, w, h, kind, depth, rows_bytes, bpp, filt, level, extra=b''):
    # Filter each row with the given filter type, by hand.
    out = bytearray()
    prev = bytes(len(rows_bytes[0]))
    for row in rows_bytes:
        f = filt if filt >= 0 else random.randint(0, 4)
        enc = bytearray()
        for i, x in enumerate(row):
            a = row[i-bpp] if i >= bpp else 0
            b = prev[i]
            c = prev[i-bpp] if i >= bpp else 0
            if f == 0: p = 0
            elif f == 1: p = a
            elif f == 2: p = b
            elif f == 3: p = (a+b)//2
            else:
                pa, pb, pc = abs(b-c), abs(a-c), abs(a+b-2*c)
                p = a if pa <= pb and pa <= pc else (b if pb <= pc else c)
            enc.append((x - p) & 255)
        out.append(f); out += enc
        prev = row
    ihdr = struct.pack('>IIBBBBB', w, h, depth, kind, 0, 0, 0)
    data = zlib.compress(bytes(out), level)
    with open(path, 'wb') as fh:
        fh.write(b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + extra + chunk(b'IDAT', data[:len(data)//2]) + chunk(b'IDAT', data[len(data)//2:]) + chunk(b'IEND', b''))

cases = []
px = [(random.randrange(256), random.randrange(256), random.randrange(256), random.randrange(256)) for _ in range(W*H)]
rows = lambda ch, fn: [bytes(v for p in px[y*W:(y+1)*W] for v in fn(p)) for y in range(H)]

for filt in [0, 1, 2, 3, 4, -1]:
    for level in [0, 1, 9]:
        name = f'rgba_f{filt}_z{level}.png'
        raw_png(name, W, H, 6, 8, rows(4, lambda p: p), 4, filt, level)
        cases.append((name, expected(px, W, H)))
raw_png('rgb.png', W, H, 2, 8, rows(3, lambda p: p[:3]), 3, -1, 6)
cases.append(('rgb.png', expected([(r,g,b,255) for r,g,b,a in px], W, H)))
raw_png('grey.png', W, H, 0, 8, rows(1, lambda p: p[:1]), 1, -1, 6)
cases.append(('grey.png', expected([(r,r,r,255) for r,g,b,a in px], W, H)))
raw_png('greya.png', W, H, 4, 8, rows(2, lambda p: (p[0], p[3])), 2, -1, 6)
cases.append(('greya.png', expected([(r,r,r,a) for r,g,b,a in px], W, H)))

for depth in [1, 2, 4, 8]:
    n = 1 << depth
    pal = [(random.randrange(256), random.randrange(256), random.randrange(256)) for _ in range(n)]
    alph = [random.randrange(256) for _ in range(max(1, n//2))]
    idx = [random.randrange(n) for _ in range(W*H)]
    rows_b = []
    for y in range(H):
        bits = 0; nb = 0; row = bytearray()
        for x in range(W):
            bits = (bits << depth) | idx[y*W+x]; nb += depth
            if nb == 8: row.append(bits); bits = 0; nb = 0
        if nb: row.append(bits << (8-nb))
        rows_b.append(bytes(row))
    extra = chunk(b'PLTE', bytes(v for c in pal for v in c)) + chunk(b'tRNS', bytes(alph))
    name = f'pal{depth}.png'
    raw_png(name, W, H, 3, depth, rows_b, 1, -1, 9, extra)
    cases.append((name, expected([(*pal[i], alph[i] if i < len(alph) else 255) for i in idx], W, H)))

# One from Pillow itself, with its own filter choices.
img = Image.new('RGBA', (W, H)); img.putdata(px); img.save('pillow.png', optimize=True)
cases.append(('pillow.png', expected(px, W, H)))
# And a 16-bit one, which has to be refused.
Image.new('I;16', (4, 4)).save('deep.png')

with open('cases.scd', 'w') as fh:
    fh.write('const cases = [\n')
    for n, h in cases:
        fh.write(f'    ["{n}", "{h}"],\n')
    fh.write(']\n')
print(len(cases), 'cases')
