/*
 * veylmac.c - on-screen windows and input for macOS.
 *
 * The counterpart to veylwin.c (which does this for Linux through X11).
 * macOS draws with Cocoa, which is Objective-C, but this is plain C: the
 * Objective-C runtime and the AppKit and CoreGraphics frameworks are all
 * loaded with dlopen at run time and called through objc_msgSend, so there
 * is no Objective-C compiler, no framework to link against, and no macOS
 * SDK needed to build it. A Mac already has all of them.
 *
 * The drawing itself is still veylgdi.c, shared with Linux: a window owns
 * a GDI bitmap that win.present blits into, and __vy_dc_dirty then shows
 * that bitmap on screen - here by handing its pixels to a CoreGraphics
 * image and setting it as the window view's layer contents.
 *
 * Sound is not wired up yet on macOS (it would be CoreAudio); the calls
 * succeed silently so a program that plays sound still runs.
 *
 * EXPERIMENTAL: built and shipped without a Mac to test on. The structure
 * is right; details may need a round of fixing against a real machine.
 */

#define _DARWIN_C_SOURCE
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MS __attribute__((ms_abi))
typedef uint32_t u32;
typedef int32_t i32;

/* From veylgdi.c (the shared software drawing). */
typedef struct bmp {
	int kind;
	int w, h;
	u32 *px;
	int ownpx;
} bmp;
MS void *__vyw_CreateCompatibleDC(void *hdc);
MS void *__vyw_CreateCompatibleBitmap(void *hdc, int w, int h);
MS void *__vyw_SelectObject(void *hdc, void *obj);
bmp *__vy_dc_target(void *hdc);
void *__vy_dc_window(void *hdc);
void __vy_dc_set_window(void *hdc, void *win);

/* ---- the Objective-C runtime and frameworks, via dlopen ----------- */

typedef void *id;
typedef void *SEL;
typedef void *Class;

typedef struct {
	double x, y;
} NSPoint;
typedef struct {
	double w, h;
} NSSize;
typedef struct {
	NSPoint origin;
	NSSize size;
} NSRect;

static struct objc {
	int ok;
	id (*msgSend)(id, SEL, ...);
	void (*msgSend_stret)(void *, id, SEL, ...);
	Class (*getClass)(const char *);
	SEL (*sel)(const char *);
} OC;

/* CoreGraphics, for turning a pixel buffer into something a layer shows. */
static struct cg {
	void *(*ColorSpaceCreateDeviceRGB)(void);
	void (*ColorSpaceRelease)(void *);
	void *(*DataProviderCreateWithData)(void *, const void *, size_t, void *);
	void (*DataProviderRelease)(void *);
	void *(*ImageCreate)(size_t, size_t, size_t, size_t, size_t, void *, u32,
			     void *, const double *, int, int);
	void (*ImageRelease)(void *);
	void (*WarpMouseCursorPosition)(NSPoint);
	void (*DisplayHideCursor)(u32);
	void (*DisplayShowCursor)(u32);
	u32 (*MainDisplayID)(void);
} CG;

static int mac_init(void)
{
	if (OC.ok)
		return OC.ok > 0;
	OC.ok = -1;
	void *objc = dlopen("/usr/lib/libobjc.A.dylib", RTLD_NOW | RTLD_GLOBAL);
	if (!objc)
		return 0;
	OC.msgSend = dlsym(objc, "objc_msgSend");
	OC.msgSend_stret = dlsym(objc, "objc_msgSend_stret");
	OC.getClass = dlsym(objc, "objc_getClass");
	OC.sel = dlsym(objc, "sel_registerName");
	if (!OC.msgSend || !OC.getClass || !OC.sel)
		return 0;
	dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", RTLD_NOW | RTLD_GLOBAL);
	dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", RTLD_NOW | RTLD_GLOBAL);
	void *cg = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", RTLD_NOW | RTLD_GLOBAL);
	if (!cg)
		cg = dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", RTLD_NOW | RTLD_GLOBAL);
	if (cg) {
		CG.ColorSpaceCreateDeviceRGB = dlsym(cg, "CGColorSpaceCreateDeviceRGB");
		CG.ColorSpaceRelease = dlsym(cg, "CGColorSpaceRelease");
		CG.DataProviderCreateWithData = dlsym(cg, "CGDataProviderCreateWithData");
		CG.DataProviderRelease = dlsym(cg, "CGDataProviderRelease");
		CG.ImageCreate = dlsym(cg, "CGImageCreate");
		CG.ImageRelease = dlsym(cg, "CGImageRelease");
		CG.WarpMouseCursorPosition = dlsym(cg, "CGWarpMouseCursorPosition");
		CG.DisplayHideCursor = dlsym(cg, "CGDisplayHideCursor");
		CG.DisplayShowCursor = dlsym(cg, "CGDisplayShowCursor");
		CG.MainDisplayID = dlsym(cg, "CGMainDisplayID");
	}
	OC.ok = 1;
	return 1;
}

/* Typed wrappers over objc_msgSend. objc_msgSend is one symbol; each call
 * shape casts it to the right prototype. */
static id send0(id o, const char *s) { return OC.msgSend(o, OC.sel(s)); }
static id sendP(id o, const char *s, void *a)
{
	id (*f)(id, SEL, void *) = (id (*)(id, SEL, void *))OC.msgSend;
	return f(o, OC.sel(s), a);
}
static id sendL(id o, const char *s, long a)
{
	id (*f)(id, SEL, long) = (id (*)(id, SEL, long))OC.msgSend;
	return f(o, OC.sel(s), a);
}
static Class cls(const char *name) { return OC.getClass(name); }

static id nsstr(const char *s)
{
	return sendP((id)cls("NSString"), "stringWithUTF8String:", (void *)(s ? s : ""));
}

/* ---- windows ----------------------------------------------------- */

enum { OBJ_WIN = 0x200 };
#define MAX_WINDOWS 16

typedef struct window {
	int kind;
	id nswin;    /* NSWindow */
	id view;     /* its content view, layer-backed */
	id layer;    /* the CALayer we set an image on */
	void *dc;    /* GDI dc whose target is front and whose window is this */
	bmp *front;
	int w, h;
	long style;
	int destroyed;
	int pendingQuit;
	char title[128];
} window;

static window *g_windows[MAX_WINDOWS];
static int g_nwin;
static unsigned char g_async[256];
static int g_cursorCount;
static id g_app; /* NSApplication */

#define MSG_MESSAGE 8
#define MSG_WPARAM 16
#define MSG_LPARAM 24

static void app_init(void)
{
	if (g_app)
		return;
	g_app = send0((id)cls("NSApplication"), "sharedApplication");
	/* NSApplicationActivationPolicyRegular = 0 */
	sendL(g_app, "setActivationPolicy:", 0);
	send0(g_app, "finishLaunching");
	/* activateIgnoringOtherApps:YES */
	sendL(g_app, "activateIgnoringOtherApps:", 1);
}

void __vy_dc_dirty(void *hdcv)
{
	window *w = __vy_dc_window(hdcv);
	if (!w || w->destroyed || !w->layer || !CG.ImageCreate)
		return;
	bmp *b = w->front;
	size_t n = (size_t)b->w * b->h * 4;
	void *cs = CG.ColorSpaceCreateDeviceRGB();
	void *prov = CG.DataProviderCreateWithData(NULL, b->px, n, NULL);
	/* The DIB is 0x00RRGGBB, i.e. bytes B,G,R,X little-endian. 32 bits,
	 * skip-first alpha, little-endian order matches that byte layout. */
	u32 info = (5u) | (1u << 12); /* kCGImageAlphaNoneSkipFirst | ByteOrder32Little */
	void *img = CG.ImageCreate((size_t)b->w, (size_t)b->h, 8, 32, (size_t)b->w * 4,
				   cs, info, prov, NULL, 0, 0);
	if (img) {
		sendP(w->layer, "setContents:", img);
		CG.ImageRelease(img);
	}
	CG.DataProviderRelease(prov);
	CG.ColorSpaceRelease(cs);
}

static void win_make_surface(window *w, int width, int height)
{
	w->w = width;
	w->h = height;
	w->front = __vyw_CreateCompatibleBitmap(NULL, width, height);
	w->dc = __vyw_CreateCompatibleDC(NULL);
	__vyw_SelectObject(w->dc, w->front);
	__vy_dc_set_window(w->dc, w);
}

MS void *__vyw_CreateWindowExA(u32 exStyle, const char *clsName, const char *title, u32 style,
			       int x, int y, int w, int h, void *parent, void *menu,
			       void *inst, void *param)
{
	(void)exStyle; (void)clsName; (void)style; (void)x; (void)y; (void)parent;
	(void)menu; (void)inst; (void)param;
	if (!mac_init() || g_nwin >= MAX_WINDOWS)
		return NULL;
	if (w <= 0) w = 640;
	if (h <= 0) h = 480;
	app_init();

	window *win = calloc(1, sizeof *win);
	win->kind = OBJ_WIN;
	win->style = style;

	NSRect frame = {{100, 100}, {(double)w, (double)h}};
	/* styleMask: Titled|Closable|Miniaturizable = 1|2|4 = 7. backing
	 * Buffered = 2. defer NO. */
	id win_alloc = send0((id)cls("NSWindow"), "alloc");
	id (*initWin)(id, SEL, NSRect, unsigned long, unsigned long, signed char) =
		(id (*)(id, SEL, NSRect, unsigned long, unsigned long, signed char))OC.msgSend;
	win->nswin = initWin(win_alloc, OC.sel("initWithContentRect:styleMask:backing:defer:"),
			     frame, 7, 2, 0);
	sendP(win->nswin, "setTitle:", nsstr(title));

	win->view = send0(win->nswin, "contentView");
	sendL(win->view, "setWantsLayer:", 1);
	win->layer = send0(win->view, "layer");
	/* draw the image without smoothing, so pixels stay crisp: not set
	 * here to keep the call set small; the default is acceptable. */

	sendP(win->nswin, "makeKeyAndOrderFront:", NULL);
	win_make_surface(win, w, h);
	g_windows[g_nwin++] = win;
	return win;
}

MS int __vyw_RegisterClassA(void *wc) { (void)wc; return 1; }
MS long __vyw_DefWindowProcA(void *h, u32 m, long wp, long lp) { (void)h; (void)m; (void)wp; (void)lp; return 0; }
MS void *__vyw_LoadCursorA(void *inst, int id_) { (void)inst; (void)id_; return (void *)1; }
MS int __vyw_ShowWindow(void *h, int cmd) { (void)h; (void)cmd; return 1; }
MS int __vyw_UpdateWindow(void *h) { (void)h; return 1; }
MS int __vyw_AdjustWindowRect(i32 *r, u32 style, int menu) { (void)r; (void)style; (void)menu; return 1; }
MS void *__vyw_GetDC(void *h) { window *w = h; return w ? w->dc : NULL; }
MS int __vyw_ReleaseDC(void *h, void *dc) { (void)h; (void)dc; return 1; }

MS int __vyw_DestroyWindow(void *h)
{
	window *w = h;
	if (!w || w->destroyed)
		return 0;
	w->destroyed = 1;
	if (w->nswin)
		send0(w->nswin, "close");
	return 1;
}

MS int __vyw_IsWindow(void *h)
{
	window *w = h;
	if (!w)
		return 0;
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] == w)
			return !w->destroyed;
	return 0;
}

MS int __vyw_GetClientRect(void *h, i32 *rect)
{
	window *w = h;
	if (!rect)
		return 0;
	rect[0] = 0;
	rect[1] = 0;
	rect[2] = w ? w->w : 0;
	rect[3] = w ? w->h : 0;
	return 1;
}

MS int __vyw_SetWindowTextA(void *h, const char *t)
{
	window *w = h;
	if (w && w->nswin && t) {
		snprintf(w->title, sizeof w->title, "%s", t);
		sendP(w->nswin, "setTitle:", nsstr(t));
	}
	return 1;
}

/* macOS hardware key code to Windows virtual-key code, for the common
 * game keys. These are the kVK_ANSI_* constants. */
static int vk_of(unsigned short kc)
{
	switch (kc) {
	case 0: return 'A';
	case 1: return 'S';
	case 2: return 'D';
	case 13: return 'W';
	case 12: return 'Q';
	case 14: return 'E';
	case 15: return 'R';
	case 3: return 'F';
	case 49: return 0x20;  /* space */
	case 53: return 0x1B;  /* escape */
	case 36: return 0x0D;  /* return */
	case 48: return 0x09;  /* tab */
	case 56: case 60: return 0x10; /* shift */
	case 59: case 62: return 0x11; /* control */
	case 58: case 61: return 0x12; /* option/alt */
	case 123: return 0x25; /* left */
	case 126: return 0x26; /* up */
	case 124: return 0x27; /* right */
	case 125: return 0x28; /* down */
	case 18: return '1';
	case 19: return '2';
	case 20: return '3';
	case 21: return '4';
	case 23: return '5';
	case 22: return '6';
	case 26: return '7';
	case 28: return '8';
	case 25: return '9';
	case 29: return '0';
	case 122: return 0x74; /* F5 */
	case 103: return 0x7A; /* F11 */
	}
	return 0;
}

static NSPoint send_point(id o, const char *s)
{
	NSPoint (*f)(id, SEL) = (NSPoint (*)(id, SEL))OC.msgSend;
	return f(o, OC.sel(s));
}

MS int __vyw_PeekMessageA(unsigned char *msg, void *hwnd, u32 min, u32 max, u32 remove)
{
	(void)hwnd; (void)min; (void)max; (void)remove;
	if (!g_app || !msg)
		return 0;
	for (int i = 0; i < g_nwin; i++) {
		if (g_windows[i] && g_windows[i]->pendingQuit) {
			g_windows[i]->pendingQuit = 0;
			memset(msg, 0, 48);
			*(u32 *)(msg + MSG_MESSAGE) = 0x0012; /* WM_QUIT */
			return 1;
		}
	}
	/* nextEventMatchingMask:NSUIntegerMax untilDate:distantPast
	 * inMode:"kCFRunLoopDefaultMode" dequeue:YES */
	id past = send0((id)cls("NSDate"), "distantPast");
	id (*nextEvent)(id, SEL, unsigned long, id, id, signed char) =
		(id (*)(id, SEL, unsigned long, id, id, signed char))OC.msgSend;
	id ev = nextEvent(g_app, OC.sel("nextEventMatchingMask:untilDate:inMode:dequeue:"),
			  (unsigned long)-1, past, nsstr("kCFRunLoopDefaultMode"), 1);
	if (!ev)
		return 0;

	long type = (long)sendL(ev, "type", 0); /* read type (no arg; cast harmless) */
	type = (long)send0(ev, "type");
	window *w = g_nwin ? g_windows[0] : NULL; /* single game window */
	memset(msg, 0, 48);

	/* NSEventType: LeftMouseDown=1 Up=2 RightDown=3 Up=4 MouseMoved=5
	 * LeftDragged=6 RightDragged=7 KeyDown=10 KeyUp=11 */
	switch (type) {
	case 5: case 6: case 7: {
		NSPoint p = send_point(ev, "locationInWindow");
		int mx = (int)p.x, my = w ? w->h - (int)p.y : (int)p.y; /* flip y */
		*(u32 *)(msg + MSG_MESSAGE) = 0x0200;
		*(long *)(msg + MSG_LPARAM) = (mx & 0xFFFF) | ((long)(my & 0xFFFF) << 16);
		break;
	}
	case 1: *(u32 *)(msg + MSG_MESSAGE) = 0x0201; g_async[1] = 0x80; break;
	case 2: *(u32 *)(msg + MSG_MESSAGE) = 0x0202; g_async[1] = 0; break;
	case 3: *(u32 *)(msg + MSG_MESSAGE) = 0x0204; g_async[2] = 0x80; break;
	case 4: *(u32 *)(msg + MSG_MESSAGE) = 0x0205; g_async[2] = 0; break;
	case 10: case 11: {
		unsigned short kc = (unsigned short)(long)send0(ev, "keyCode");
		int vk = vk_of(kc);
		if (vk) {
			g_async[vk & 0xFF] = type == 10 ? 0x80 : 0;
			*(u32 *)(msg + MSG_MESSAGE) = type == 10 ? 0x0100 : 0x0101;
			*(long *)(msg + MSG_WPARAM) = vk;
		}
		break;
	}
	default:
		break;
	}
	sendP(g_app, "sendEvent:", ev);
	return 1;
}

MS int __vyw_TranslateMessage(void *msg) { (void)msg; return 0; }
MS long __vyw_DispatchMessageA(void *msg) { (void)msg; return 0; }

MS int __vyw_GetAsyncKeyState(int vk)
{
	if (g_app) {
		unsigned char m[48];
		while (__vyw_PeekMessageA(m, NULL, 0, 0, 1))
			;
	}
	return (vk >= 0 && vk < 256 && g_async[vk]) ? 0x8000 : 0;
}

MS int __vyw_GetSystemMetrics(int i)
{
	if (!mac_init())
		return i == 0 ? 1920 : 1080;
	id screen = send0((id)cls("NSScreen"), "mainScreen");
	if (!screen || !OC.msgSend_stret)
		return i == 0 ? 1920 : 1080;
	NSRect r;
	OC.msgSend_stret(&r, screen, OC.sel("frame"));
	return i == 0 ? (int)r.size.w : (int)r.size.h;
}

MS int __vyw_GetCursorPos(i32 *pt)
{
	if (!mac_init() || !pt)
		return 0;
	id loc_cls = (id)cls("NSEvent");
	NSPoint p = send_point(loc_cls, "mouseLocation");
	int sh = __vyw_GetSystemMetrics(1);
	pt[0] = (int)p.x;
	pt[1] = sh - (int)p.y; /* screen coords, top-left origin */
	return 1;
}

MS int __vyw_SetCursorPos(int x, int y)
{
	if (!mac_init() || !CG.WarpMouseCursorPosition)
		return 0;
	NSPoint p = {(double)x, (double)y};
	CG.WarpMouseCursorPosition(p);
	return 1;
}

MS int __vyw_ShowCursor(int show)
{
	g_cursorCount += show ? 1 : -1;
	if (CG.DisplayHideCursor && CG.DisplayShowCursor) {
		u32 d = CG.MainDisplayID ? CG.MainDisplayID() : 0;
		if (g_cursorCount < 0)
			CG.DisplayHideCursor(d);
		else
			CG.DisplayShowCursor(d);
	}
	return g_cursorCount;
}

MS void *__vyw_GetActiveWindow(void)
{
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] && !g_windows[i]->destroyed &&
		    (long)send0(g_windows[i]->nswin, "isKeyWindow"))
			return g_windows[i];
	return NULL;
}

MS void *__vyw_FindWindowA(const char *clsName, const char *title)
{
	(void)clsName;
	if (!title)
		return NULL;
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] && !strcmp(g_windows[i]->title, title))
			return g_windows[i];
	return NULL;
}

MS long __vyw_GetWindowLongPtrA(void *h, int idx) { window *w = h; (void)idx; return w ? w->style : 0; }
MS long __vyw_SetWindowLongPtrA(void *h, int idx, long v)
{
	window *w = h;
	(void)idx;
	if (!w)
		return 0;
	long old = w->style;
	w->style = v;
	return old;
}
MS int __vyw_SetWindowPos(void *h, void *after, int x, int y, int cx, int cy, u32 flags)
{
	(void)h; (void)after; (void)x; (void)y; (void)cx; (void)cy; (void)flags;
	return 1;
}
MS int __vyw_GetWindowRect(void *h, i32 *rect)
{
	window *w = h;
	if (!rect || !w)
		return 0;
	rect[0] = 0; rect[1] = 0; rect[2] = w->w; rect[3] = w->h;
	return 1;
}

typedef MS int (*enumproc)(void *hwnd, long param);
MS int __vyw_EnumWindows(enumproc cb, long param)
{
	if (!cb)
		return 0;
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] && !g_windows[i]->destroyed)
			if (!cb(g_windows[i], param))
				break;
	return 1;
}

/* module and symbol lookup, for mem.symbol and extern-through-address */
MS void *__vyw_LoadLibraryA(const char *name)
{
	char so[512];
	if (!name)
		return dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);
	void *h = dlopen(name, RTLD_NOW | RTLD_GLOBAL);
	if (!h) {
		snprintf(so, sizeof so, "lib%s.dylib", name);
		h = dlopen(so, RTLD_NOW | RTLD_GLOBAL);
	}
	return h;
}
MS void *__vyw_GetModuleHandleA(const char *name) { (void)name; return dlopen(NULL, RTLD_NOW | RTLD_GLOBAL); }
MS void *__vyw_GetProcAddress(void *mod, const char *name)
{
	if (!mod || !name)
		return NULL;
	return dlsym(mod, name);
}

/* ---- sound (not wired up yet; calls succeed silently) ------------ */

MS int __vyw_PlaySoundA(const char *path, void *mod, u32 flags) { (void)path; (void)mod; (void)flags; return 1; }
MS int __vyw_mciSendStringA(const char *cmd, char *ret, int n, void *cb) { (void)cmd; (void)ret; (void)n; (void)cb; return 0; }
