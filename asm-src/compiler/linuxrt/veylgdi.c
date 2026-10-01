/*
 * veylgdi.c - the drawing half of the Windows libraries, on Linux.
 *
 * The win library (win.go, winmedia.go) lowers to GDI and user32 calls:
 * device contexts, bitmaps, FillRect, lines, ellipses, text, and the
 * blits that put one bitmap on another. None of that exists on Linux, so
 * it is implemented here in software, over plain pixel buffers, under the
 * Windows calling convention like the rest of the Linux runtime
 * (veylrt.c). An off-screen canvas - win.canvas - needs only this file
 * and no display at all; a real on-screen window adds X11 on top, in
 * veylwin.c.
 *
 * The pixel model matches what the Windows target uses so a program's
 * numbers come out identical. A DIB pixel is a 32-bit 0x00RRGGBB stored
 * top-down, which is what win.pixels hands back. A COLORREF - what
 * win.rgb makes, and what brushes, pens and GetPixel speak - is
 * 0x00BBGGRR, the red and blue the other way round, exactly as on
 * Windows. The two conversions below are the whole of it.
 */

#define _GNU_SOURCE
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

#define MS __attribute__((ms_abi))
typedef uint32_t u32;
typedef int32_t i32;

/* ---- objects ----------------------------------------------------- */

enum { OBJ_DC = 0x100, OBJ_BMP, OBJ_BRUSH, OBJ_PEN };

typedef struct bmp {
	int kind;
	int w, h;
	u32 *px;   /* w*h, 0x00RRGGBB, top-down */
	int ownpx; /* whether px is ours to free */
} bmp;

typedef struct brush {
	int kind;
	u32 color; /* COLORREF */
} brush;

typedef struct pen {
	int kind;
	u32 color; /* COLORREF */
	int width;
} pen;

typedef struct dc {
	int kind;
	bmp *target;
	u32 textColor; /* COLORREF */
	int bkMode;
	int curX, curY;
	pen *curPen;
	brush *curBrush;
	void *window; /* non-NULL for a window's DC; see veylwin.c */
} dc;

/* The two kinds in play are flagged so SelectObject can tell them apart. */
static int obj_kind(void *p) { return p ? *(int *)p : 0; }

static u32 cr_to_dib(u32 cr)
{
	return ((cr & 0xFF) << 16) | (cr & 0xFF00) | ((cr >> 16) & 0xFF);
}
static u32 dib_to_cr(u32 d)
{
	return ((d & 0xFF) << 16) | (d & 0xFF00) | ((d >> 16) & 0xFF);
}

/* The window code reaches a window's bitmap and marks it dirty through
 * these, so this file need not know what a window is. */
bmp *__vy_dc_target(void *hdc) { return hdc ? ((dc *)hdc)->target : NULL; }
void *__vy_dc_window(void *hdc) { return hdc ? ((dc *)hdc)->window : NULL; }
void __vy_dc_set_window(void *hdc, void *win) { if (hdc) ((dc *)hdc)->window = win; }

/* Defined in veylwin.c; a no-op there is fine for off-screen drawing. */
void __vy_dc_dirty(void *hdc);

/* ---- device contexts and bitmaps --------------------------------- */

MS void *__vyw_CreateCompatibleDC(void *hdc)
{
	(void)hdc;
	dc *d = calloc(1, sizeof *d);
	d->kind = OBJ_DC;
	d->bkMode = 2; /* OPAQUE */
	return d;
}

MS void *__vyw_GetStockObject(int which)
{
	/* Only the white background brush is asked for, by win.open. */
	(void)which;
	static brush white = {OBJ_BRUSH, 0x00FFFFFF};
	return &white;
}

static bmp *new_bmp(int w, int h)
{
	bmp *b = calloc(1, sizeof *b);
	b->kind = OBJ_BMP;
	b->w = w < 1 ? 1 : w;
	b->h = h < 1 ? 1 : h;
	b->px = calloc((size_t)b->w * b->h, 4);
	b->ownpx = 1;
	return b;
}

MS void *__vyw_CreateCompatibleBitmap(void *hdc, int w, int h)
{
	(void)hdc;
	return new_bmp(w, h);
}

/* CreateDIBSection: read the header the compiler fills in (biWidth at 4,
 * biHeight at 8 - negative for top-down - biBitCount at 14), make the
 * bitmap, and hand its pixel pointer back through bits. */
MS void *__vyw_CreateDIBSection(void *hdc, const unsigned char *bmi, u32 usage,
				void **bits, void *section, u32 offset)
{
	(void)hdc; (void)usage; (void)section; (void)offset;
	i32 w = (i32)(bmi[4] | bmi[5] << 8 | bmi[6] << 16 | (u32)bmi[7] << 24);
	i32 h = (i32)(bmi[8] | bmi[9] << 8 | bmi[10] << 16 | (u32)bmi[11] << 24);
	if (w < 0)
		w = -w;
	if (h < 0)
		h = -h;
	bmp *b = new_bmp(w, h);
	if (bits)
		*bits = b->px;
	return b;
}

MS void *__vyw_SelectObject(void *hdc, void *obj)
{
	dc *d = hdc;
	if (!d)
		return NULL;
	switch (obj_kind(obj)) {
	case OBJ_BMP: {
		bmp *old = d->target;
		d->target = obj;
		return old;
	}
	case OBJ_PEN: {
		pen *old = d->curPen;
		d->curPen = obj;
		return old;
	}
	case OBJ_BRUSH: {
		brush *old = d->curBrush;
		d->curBrush = obj;
		return old;
	}
	}
	return NULL;
}

MS int __vyw_DeleteObject(void *obj)
{
	switch (obj_kind(obj)) {
	case OBJ_BMP: {
		bmp *b = obj;
		if (b->ownpx)
			free(b->px);
		free(b);
		return 1;
	}
	case OBJ_PEN:
	case OBJ_BRUSH:
		free(obj);
		return 1;
	}
	return 0;
}

MS int __vyw_DeleteDC(void *hdc)
{
	free(hdc);
	return 1;
}

MS void *__vyw_CreateSolidBrush(u32 color)
{
	brush *b = calloc(1, sizeof *b);
	b->kind = OBJ_BRUSH;
	b->color = color;
	return b;
}

MS void *__vyw_CreatePen(int style, int width, u32 color)
{
	(void)style;
	pen *p = calloc(1, sizeof *p);
	p->kind = OBJ_PEN;
	p->color = color;
	p->width = width < 1 ? 1 : width;
	return p;
}

MS int __vyw_SetBkMode(void *hdc, int mode)
{
	dc *d = hdc;
	int old = d ? d->bkMode : 0;
	if (d)
		d->bkMode = mode;
	return old;
}

MS u32 __vyw_SetTextColor(void *hdc, u32 color)
{
	dc *d = hdc;
	u32 old = d ? d->textColor : 0;
	if (d)
		d->textColor = color;
	return old;
}

/* ---- the raster primitives --------------------------------------- */

static inline void put(bmp *b, int x, int y, u32 dib)
{
	if (b && (unsigned)x < (unsigned)b->w && (unsigned)y < (unsigned)b->h)
		b->px[(size_t)y * b->w + x] = dib;
}

/* FillRect excludes the right and bottom edges, as on Windows. */
MS int __vyw_FillRect(void *hdc, const i32 *rect, void *hbrush)
{
	dc *d = hdc;
	if (!d || !d->target || !rect)
		return 0;
	u32 dib = cr_to_dib(obj_kind(hbrush) == OBJ_BRUSH ? ((brush *)hbrush)->color : 0);
	int x0 = rect[0], y0 = rect[1], x1 = rect[2], y1 = rect[3];
	if (x0 < 0) x0 = 0;
	if (y0 < 0) y0 = 0;
	if (x1 > d->target->w) x1 = d->target->w;
	if (y1 > d->target->h) y1 = d->target->h;
	for (int y = y0; y < y1; y++)
		for (int x = x0; x < x1; x++)
			d->target->px[(size_t)y * d->target->w + x] = dib;
	__vy_dc_dirty(d);
	return 1;
}

MS int __vyw_MoveToEx(void *hdc, int x, int y, i32 *old)
{
	dc *d = hdc;
	if (!d)
		return 0;
	if (old) {
		old[0] = d->curX;
		old[1] = d->curY;
	}
	d->curX = x;
	d->curY = y;
	return 1;
}

static void line(bmp *b, int x0, int y0, int x1, int y1, u32 dib)
{
	int dx = abs(x1 - x0), sx = x0 < x1 ? 1 : -1;
	int dy = -abs(y1 - y0), sy = y0 < y1 ? 1 : -1;
	int err = dx + dy;
	for (;;) {
		put(b, x0, y0, dib);
		if (x0 == x1 && y0 == y1)
			break;
		int e2 = 2 * err;
		if (e2 >= dy) {
			err += dy;
			x0 += sx;
		}
		if (e2 <= dx) {
			err += dx;
			y0 += sy;
		}
	}
}

MS int __vyw_LineTo(void *hdc, int x, int y)
{
	dc *d = hdc;
	if (!d || !d->target)
		return 0;
	u32 cr = d->curPen ? d->curPen->color : 0;
	line(d->target, d->curX, d->curY, x, y, cr_to_dib(cr));
	d->curX = x;
	d->curY = y;
	__vy_dc_dirty(d);
	return 1;
}

/* A filled ellipse inside the box (x0,y0)-(x1,y1), outlined with the pen,
 * which is what win.circle asks for. The right and bottom edges are
 * exclusive, as GDI's Ellipse treats them. */
MS int __vyw_Ellipse(void *hdc, int x0, int y0, int x1, int y1)
{
	dc *d = hdc;
	if (!d || !d->target)
		return 0;
	if (x1 < x0) { int t = x0; x0 = x1; x1 = t; }
	if (y1 < y0) { int t = y0; y0 = y1; y1 = t; }
	double cx = (x0 + x1 - 1) / 2.0, cy = (y0 + y1 - 1) / 2.0;
	double rx = (x1 - x0 - 1) / 2.0, ry = (y1 - y0 - 1) / 2.0;
	if (rx < 0.5) rx = 0.5;
	if (ry < 0.5) ry = 0.5;
	u32 fill = cr_to_dib(d->curBrush ? d->curBrush->color : 0x00FFFFFF);
	u32 edge = cr_to_dib(d->curPen ? d->curPen->color : 0);
	for (int y = y0; y < y1; y++) {
		double ny = (y - cy) / ry;
		for (int x = x0; x < x1; x++) {
			double nx = (x - cx) / rx;
			double r = nx * nx + ny * ny;
			if (r <= 1.0)
				put(d->target, x, y, r > 0.78 ? edge : fill);
		}
	}
	__vy_dc_dirty(d);
	return 1;
}

MS int __vyw_Rectangle(void *hdc, int x0, int y0, int x1, int y1)
{
	dc *d = hdc;
	if (!d || !d->target)
		return 0;
	u32 fill = cr_to_dib(d->curBrush ? d->curBrush->color : 0x00FFFFFF);
	for (int y = y0; y < y1; y++)
		for (int x = x0; x < x1; x++)
			put(d->target, x, y, fill);
	u32 edge = cr_to_dib(d->curPen ? d->curPen->color : 0);
	line(d->target, x0, y0, x1 - 1, y0, edge);
	line(d->target, x0, y1 - 1, x1 - 1, y1 - 1, edge);
	line(d->target, x0, y0, x0, y1 - 1, edge);
	line(d->target, x1 - 1, y0, x1 - 1, y1 - 1, edge);
	__vy_dc_dirty(d);
	return 1;
}

MS u32 __vyw_GetPixel(void *hdc, int x, int y)
{
	dc *d = hdc;
	if (!d || !d->target || (unsigned)x >= (unsigned)d->target->w ||
	    (unsigned)y >= (unsigned)d->target->h)
		return 0xFFFFFFFFu; /* CLR_INVALID */
	return dib_to_cr(d->target->px[(size_t)y * d->target->w + x]);
}

/* GetObjectA on a bitmap fills a BITMAP: type 0, width at 4, height at 8,
 * then stride and the rest, which win.go reads width and height out of. */
MS int __vyw_GetObjectA(void *obj, int cb, unsigned char *buf)
{
	if (obj_kind(obj) != OBJ_BMP || cb < 20 || !buf)
		return 0;
	bmp *b = obj;
	memset(buf, 0, 20);
	i32 w = b->w, h = b->h, stride = b->w * 4;
	memcpy(buf + 4, &w, 4);
	memcpy(buf + 8, &h, 4);
	memcpy(buf + 12, &stride, 4);
	buf[16] = 1;  /* planes */
	buf[18] = 32; /* bits */
	return 20;
}

MS void __vyw_GdiFlush(void) {}

/* ---- blits ------------------------------------------------------- */

MS int __vyw_BitBlt(void *hdst, int dx, int dy, int w, int h,
		    void *hsrc, int sx, int sy, u32 rop)
{
	(void)rop;
	dc *dd = hdst, *sd = hsrc;
	if (!dd || !sd || !dd->target || !sd->target)
		return 0;
	for (int y = 0; y < h; y++)
		for (int x = 0; x < w; x++)
			if ((unsigned)(sx + x) < (unsigned)sd->target->w &&
			    (unsigned)(sy + y) < (unsigned)sd->target->h)
				put(dd->target, dx + x, dy + y,
				    sd->target->px[(size_t)(sy + y) * sd->target->w + sx + x]);
	__vy_dc_dirty(dd);
	return 1;
}

static u32 sample(bmp *s, int sx, int sy, int sw, int sh, int x, int w, int y, int h)
{
	int u = sx + (w ? x * sw / w : 0);
	int v = sy + (h ? y * sh / h : 0);
	if ((unsigned)u >= (unsigned)s->w || (unsigned)v >= (unsigned)s->h)
		return 0;
	return s->px[(size_t)v * s->w + u];
}

MS int __vyw_StretchBlt(void *hdst, int dx, int dy, int dw, int dh,
			void *hsrc, int sx, int sy, int sw, int sh, u32 rop)
{
	(void)rop;
	dc *dd = hdst, *sd = hsrc;
	if (!dd || !sd || !dd->target || !sd->target)
		return 0;
	for (int y = 0; y < dh; y++)
		for (int x = 0; x < dw; x++)
			put(dd->target, dx + x, dy + y, sample(sd->target, sx, sy, sw, sh, x, dw, y, dh));
	__vy_dc_dirty(dd);
	return 1;
}

MS int __vyw_TransparentBlt(void *hdst, int dx, int dy, int dw, int dh,
			    void *hsrc, int sx, int sy, int sw, int sh, u32 crKey)
{
	dc *dd = hdst, *sd = hsrc;
	if (!dd || !sd || !dd->target || !sd->target)
		return 0;
	u32 key = cr_to_dib(crKey) & 0xFFFFFF;
	for (int y = 0; y < dh; y++)
		for (int x = 0; x < dw; x++) {
			u32 p = sample(sd->target, sx, sy, sw, sh, x, dw, y, dh);
			if ((p & 0xFFFFFF) != key)
				put(dd->target, dx + x, dy + y, p);
		}
	__vy_dc_dirty(dd);
	return 1;
}

/* AlphaBlend's last argument is a BLENDFUNCTION packed into 32 bits:
 * SourceConstantAlpha in byte 2, AC_SRC_ALPHA (per-pixel) in byte 3. */
MS int __vyw_AlphaBlend(void *hdst, int dx, int dy, int dw, int dh,
			void *hsrc, int sx, int sy, int sw, int sh, u32 blend)
{
	dc *dd = hdst, *sd = hsrc;
	if (!dd || !sd || !dd->target || !sd->target)
		return 0;
	int ca = (blend >> 16) & 0xFF;
	int perPixel = (blend >> 24) & 0x01;
	for (int y = 0; y < dh; y++)
		for (int x = 0; x < dw; x++) {
			u32 p = sample(sd->target, sx, sy, sw, sh, x, dw, y, dh);
			int a = perPixel ? (int)(p >> 24) : 255;
			int tx = dx + x, ty = dy + y;
			if ((unsigned)tx >= (unsigned)dd->target->w || (unsigned)ty >= (unsigned)dd->target->h)
				continue;
			u32 *d = &dd->target->px[(size_t)ty * dd->target->w + tx];
			u32 b = *d;
			/* The source is premultiplied, as AC_SRC_ALPHA requires: the
			 * destination is attenuated by the effective alpha and the
			 * source added on, each scaled by the constant alpha. */
			int sr = (((p >> 16) & 0xFF) * ca + 127) / 255;
			int sg = (((p >> 8) & 0xFF) * ca + 127) / 255;
			int sb = ((p & 0xFF) * ca + 127) / 255;
			int ea = (a * ca + 127) / 255;
			int dr = (b >> 16) & 0xFF, dg = (b >> 8) & 0xFF, db = b & 0xFF;
			dr = (dr * (255 - ea) + 127) / 255 + sr;
			dg = (dg * (255 - ea) + 127) / 255 + sg;
			db = (db * (255 - ea) + 127) / 255 + sb;
			if (dr > 255) dr = 255;
			if (dg > 255) dg = 255;
			if (db > 255) db = 255;
			*d = (dr << 16) | (dg << 8) | db;
		}
	__vy_dc_dirty(dd);
	return 1;
}

/* ---- text, in a built-in 8x8 font -------------------------------- */

#include "font8x8.h"

MS int __vyw_TextOutA(void *hdc, int x, int y, const char *s, int n)
{
	dc *d = hdc;
	if (!d || !d->target || !s)
		return 0;
	u32 fg = cr_to_dib(d->textColor);
	for (int i = 0; i < n; i++) {
		unsigned char ch = (unsigned char)s[i];
		const unsigned char *g = font8x8(ch);
		for (int row = 0; row < 8; row++)
			for (int col = 0; col < 8; col++)
				if (g[row] & (1 << col))
					put(d->target, x + i * 8 + col, y + row, fg);
	}
	__vy_dc_dirty(d);
	return 1;
}

MS int __vyw_GetTextExtentPoint32A(void *hdc, const char *s, int n, i32 *size)
{
	(void)hdc; (void)s;
	if (size) {
		size[0] = n * 8;
		size[1] = 8;
	}
	return 1;
}

/* ---- loading a BMP ----------------------------------------------- */

/* LoadImageA for the one way win.go calls it: a .bmp file from disk into
 * a DIB section. 24- and 32-bit BI_RGB only; bottom-up or top-down. */
MS void *__vyw_LoadImageA(void *inst, const char *name, u32 type, int cx, int cy, u32 flags)
{
	(void)inst; (void)type; (void)cx; (void)cy; (void)flags;
	char path[4096];
	int k = 0;
	for (const char *p = name; p && *p && k < 4095; p++)
		path[k++] = *p == '\\' ? '/' : *p;
	path[k] = 0;
	FILE *f = fopen(path, "rb");
	if (!f)
		return NULL;
	unsigned char hd[54];
	if (fread(hd, 1, 54, f) != 54 || hd[0] != 'B' || hd[1] != 'M') {
		fclose(f);
		return NULL;
	}
	u32 off = hd[10] | hd[11] << 8 | hd[12] << 16 | (u32)hd[13] << 24;
	i32 w = (i32)(hd[18] | hd[19] << 8 | hd[20] << 16 | (u32)hd[21] << 24);
	i32 h = (i32)(hd[22] | hd[23] << 8 | hd[24] << 16 | (u32)hd[25] << 24);
	int bpp = hd[28] | hd[29] << 8;
	int topDown = h < 0;
	if (h < 0)
		h = -h;
	if (w <= 0 || h <= 0 || (bpp != 24 && bpp != 32)) {
		fclose(f);
		return NULL;
	}
	bmp *b = new_bmp(w, h);
	int bytespp = bpp / 8;
	int stride = (w * bytespp + 3) & ~3;
	unsigned char *row = malloc(stride);
	if (fseek(f, (long)off, SEEK_SET) != 0)
		off = 0;
	for (int r = 0; r < h; r++) {
		if (fread(row, 1, stride, f) != (size_t)stride)
			break;
		int dy = topDown ? r : h - 1 - r;
		for (int x = 0; x < w; x++) {
			unsigned char *p = row + x * bytespp;
			u32 bl = p[0], gr = p[1], re = p[2];
			u32 al = bytespp == 4 ? p[3] : 0xFF;
			b->px[(size_t)dy * w + x] = (al << 24) | (re << 16) | (gr << 8) | bl;
		}
	}
	free(row);
	fclose(f);
	return b;
}
