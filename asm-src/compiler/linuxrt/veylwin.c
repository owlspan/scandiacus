/*
 * veylwin.c - on-screen windows, input and sound for Linux.
 *
 * veylgdi.c draws into pixel buffers with no display. This file puts a
 * buffer on the screen and reads the keyboard and mouse, through X11, and
 * plays sound through PulseAudio. Both libraries are opened at run time
 * with dlopen, so a program that never opens a window or plays a sound
 * needs neither installed, and the Linux runtime builds without their
 * headers.
 *
 * The window library (win.go) is written against user32 and gdi32: it
 * registers a class, creates a window, and pumps messages with
 * PeekMessageA, reading WM_* out of them. Those messages are synthesised
 * here from X11 events. A window's pixels live in a GDI bitmap (veylgdi.c)
 * that win.present blits to; __vy_dc_dirty then pushes that bitmap to the
 * X server.
 */

#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>
#include <stdio.h>

#define MS __attribute__((ms_abi))
typedef uint32_t u32;
typedef int32_t i32;

/* From veylgdi.c: a window's drawing surface. */
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

/* A dc, enough of it to carry the window pointer and target. The real
 * definition is in veylgdi.c; only these two fields are touched here and
 * they sit at the front, so a forward view of them is safe. */

/* ---- X11, loaded on first use ------------------------------------ */

typedef unsigned long XID;
typedef XID Window;
typedef XID Drawable;
typedef void Display;
typedef void *GC;

/* Only the fields read below are named; the union is padded to the real
 * XEvent size (192 bytes on x86-64) so XNextEvent cannot overrun. */
typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window window;
} XAnyEvent;

typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window window;
	Window root, subwindow;
	unsigned long time;
	int x, y, x_root, y_root;
	unsigned int state, keycode;
	int same_screen;
} XKeyEvent;

typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window window;
	Window root, subwindow;
	unsigned long time;
	int x, y, x_root, y_root;
	unsigned int state, button;
	int same_screen;
} XButtonEvent;

typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window window;
	Window root, subwindow;
	unsigned long time;
	int x, y, x_root, y_root;
	unsigned int state;
	char is_hint;
	int same_screen;
} XMotionEvent;

typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window event, window;
	int x, y, width, height, border_width;
	Window above;
	int override_redirect;
} XConfigureEvent;

typedef struct {
	int type;
	unsigned long serial;
	int send_event;
	Display *display;
	Window window;
	XID message_type;
	int format;
	long data[5];
} XClientMessageEvent;

typedef union {
	int type;
	XAnyEvent xany;
	XKeyEvent xkey;
	XButtonEvent xbutton;
	XMotionEvent xmotion;
	XConfigureEvent xconfigure;
	XClientMessageEvent xclient;
	long pad[24];
} XEvent;

typedef struct {
	int x, y, width, height, border_width, depth;
	void *visual;
	Window root;
	int class;
	long bit_gravity, win_gravity, backing_store;
	unsigned long backing_planes, backing_pixel;
	int save_under;
	XID colormap;
	int map_installed, map_state;
	long all_event_masks, your_event_masks, do_not_propagate_mask;
	int override_redirect;
	void *screen;
} XWindowAttributes;

/* event types and masks */
#define KeyPress 2
#define KeyRelease 3
#define ButtonPress 4
#define ButtonRelease 5
#define MotionNotify 6
#define ConfigureNotify 22
#define ClientMessage 33
#define DestroyNotify 17

#define KeyPressMask (1L << 0)
#define KeyReleaseMask (1L << 1)
#define ButtonPressMask (1L << 2)
#define ButtonReleaseMask (1L << 3)
#define PointerMotionMask (1L << 6)
#define StructureNotifyMask (1L << 17)
#define ExposureMask (1L << 15)

#define ZPixmap 2

struct x11 {
	void *lib;
	Display *(*OpenDisplay)(const char *);
	int (*CloseDisplay)(Display *);
	Window (*RootWindow)(Display *, int);
	int (*DefaultScreen)(Display *);
	void *(*DefaultVisual)(Display *, int);
	int (*DefaultDepth)(Display *, int);
	GC (*DefaultGC)(Display *, int);
	Window (*CreateSimpleWindow)(Display *, Window, int, int, unsigned, unsigned,
				     unsigned, unsigned long, unsigned long);
	int (*StoreName)(Display *, Window, const char *);
	int (*MapWindow)(Display *, Window);
	int (*UnmapWindow)(Display *, Window);
	int (*DestroyWindow)(Display *, Window);
	int (*SelectInput)(Display *, Window, long);
	int (*NextEvent)(Display *, XEvent *);
	int (*Pending)(Display *);
	int (*Flush)(Display *);
	void *(*CreateImage)(Display *, void *, unsigned depth, int format, int offset,
			     char *data, unsigned w, unsigned h, int pad, int bytes_per_line);
	int (*PutImage)(Display *, Drawable, GC, void *image, int sx, int sy, int dx, int dy,
			unsigned w, unsigned h);
	XID (*LookupKeysym)(XKeyEvent *, int);
	int (*WarpPointer)(Display *, Window, Window, int, int, unsigned, unsigned, int, int);
	int (*QueryPointer)(Display *, Window, Window *, Window *, int *, int *, int *, int *, unsigned *);
	int (*GetWindowAttributes)(Display *, Window, XWindowAttributes *);
	int (*MoveResizeWindow)(Display *, Window, int, int, unsigned, unsigned);
	int (*DisplayWidth)(Display *, int);
	int (*DisplayHeight)(Display *, int);
	XID (*InternAtom)(Display *, const char *, int);
	int (*SetWMProtocols)(Display *, Window, XID *, int);
	int (*GetInputFocus)(Display *, Window *, int *);
	int (*DefineCursor)(Display *, Window, XID);
	int (*UndefineCursor)(Display *, Window);
	int screen;
	Window root;
};

static struct x11 X;
static Display *dpy;
static int x11_tried;

#define LOAD(name) X.name = dlsym(X.lib, "X" #name)

static int x11_init(void)
{
	if (x11_tried)
		return dpy != NULL;
	x11_tried = 1;
	X.lib = dlopen("libX11.so.6", RTLD_NOW | RTLD_GLOBAL);
	if (!X.lib)
		X.lib = dlopen("libX11.so", RTLD_NOW | RTLD_GLOBAL);
	if (!X.lib)
		return 0;
	LOAD(OpenDisplay); LOAD(CloseDisplay); LOAD(RootWindow); LOAD(DefaultScreen);
	LOAD(DefaultVisual); LOAD(DefaultDepth); LOAD(DefaultGC); LOAD(CreateSimpleWindow);
	LOAD(StoreName); LOAD(MapWindow); LOAD(UnmapWindow); LOAD(DestroyWindow);
	LOAD(SelectInput); LOAD(NextEvent); LOAD(Pending); LOAD(Flush); LOAD(CreateImage);
	LOAD(PutImage); LOAD(LookupKeysym); LOAD(WarpPointer); LOAD(QueryPointer);
	LOAD(GetWindowAttributes); LOAD(MoveResizeWindow); LOAD(DisplayWidth);
	LOAD(DisplayHeight); LOAD(InternAtom); LOAD(SetWMProtocols); LOAD(GetInputFocus);
	LOAD(DefineCursor); LOAD(UndefineCursor);
	dpy = X.OpenDisplay(NULL);
	if (!dpy)
		return 0;
	X.screen = X.DefaultScreen(dpy);
	X.root = X.RootWindow(dpy, X.screen);
	return 1;
}

/* ---- windows ----------------------------------------------------- */

enum { OBJ_WIN = 0x200 };
#define MAX_WINDOWS 16

typedef struct window {
	int kind;
	Window xwin;
	void *dc;    /* a GDI dc whose target is front and whose window is this */
	bmp *front;  /* what is shown; win.present blits into it */
	void *image; /* XImage over front->px */
	int w, h;
	long style;
	int destroyed;
	int pendingQuit;
	char title[128];
} window;

static window *g_windows[MAX_WINDOWS];
static int g_nwin;
static int g_focus = -1;
static unsigned char g_async[256]; /* key/button down, by virtual-key code */
static int g_cursorCount; /* ShowCursor's counter */
static XID g_wmDelete;

/* A pending WM_* message, filled by PeekMessageA from an X event. */
#define MSG_MESSAGE 8
#define MSG_WPARAM 16
#define MSG_LPARAM 24

void __vy_dc_dirty(void *hdcv)
{
	window *w = __vy_dc_window(hdcv);
	if (!w || !dpy || !w->image || w->destroyed)
		return;
	X.PutImage(dpy, w->xwin, X.DefaultGC(dpy, X.screen), w->image, 0, 0, 0, 0,
		   (unsigned)w->w, (unsigned)w->h);
	X.Flush(dpy);
}

static void win_make_surface(window *w, int width, int height)
{
	w->w = width;
	w->h = height;
	w->front = __vyw_CreateCompatibleBitmap(NULL, width, height);
	w->dc = __vyw_CreateCompatibleDC(NULL);
	__vyw_SelectObject(w->dc, w->front);
	/* mark this dc as a window's, so draws to it reach the screen */
	__vy_dc_set_window(w->dc, w);
	w->image = X.CreateImage(dpy, X.DefaultVisual(dpy, X.screen),
				 (unsigned)X.DefaultDepth(dpy, X.screen), ZPixmap, 0,
				 (char *)w->front->px, (unsigned)width, (unsigned)height, 32,
				 width * 4);
}

MS void *__vyw_CreateWindowExA(u32 exStyle, const char *cls, const char *title, u32 style,
			       int x, int y, int w, int h, void *parent, void *menu,
			       void *inst, void *param)
{
	(void)exStyle; (void)cls; (void)style; (void)x; (void)y; (void)parent; (void)menu;
	(void)inst; (void)param;
	if (!x11_init() || g_nwin >= MAX_WINDOWS)
		return NULL;
	if (w <= 0) w = 640;
	if (h <= 0) h = 480;
	window *win = calloc(1, sizeof *win);
	win->kind = OBJ_WIN;
	win->style = style;
	win->xwin = X.CreateSimpleWindow(dpy, X.root, 0, 0, (unsigned)w, (unsigned)h, 0, 0, 0);
	if (title) {
		snprintf(win->title, sizeof win->title, "%s", title);
		X.StoreName(dpy, win->xwin, title);
	}
	X.SelectInput(dpy, win->xwin, KeyPressMask | KeyReleaseMask | ButtonPressMask |
		      ButtonReleaseMask | PointerMotionMask | StructureNotifyMask | ExposureMask);
	g_wmDelete = X.InternAtom(dpy, "WM_DELETE_WINDOW", 0);
	XID protos = g_wmDelete;
	X.SetWMProtocols(dpy, win->xwin, &protos, 1);
	win_make_surface(win, w, h);
	g_windows[g_nwin++] = win;
	g_focus = g_nwin - 1;
	return win;
}

static window *find_window(Window xw)
{
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] && g_windows[i]->xwin == xw)
			return g_windows[i];
	return NULL;
}

MS int __vyw_RegisterClassA(void *wc) { (void)wc; return 1; }
MS long __vyw_DefWindowProcA(void *h, u32 m, long wp, long lp) { (void)h; (void)m; (void)wp; (void)lp; return 0; }
MS void *__vyw_LoadCursorA(void *inst, int id) { (void)inst; (void)id; return (void *)1; }
MS int __vyw_ShowWindow(void *h, int cmd) { (void)cmd; window *w = h; if (w && dpy) X.MapWindow(dpy, w->xwin); return 1; }
MS int __vyw_UpdateWindow(void *h) { (void)h; if (dpy) X.Flush(dpy); return 1; }
MS int __vyw_AdjustWindowRect(i32 *r, u32 style, int menu) { (void)r; (void)style; (void)menu; return 1; }
MS void *__vyw_GetDC(void *h) { window *w = h; return w ? w->dc : NULL; }
MS int __vyw_ReleaseDC(void *h, void *dc) { (void)h; (void)dc; return 1; }

MS int __vyw_DestroyWindow(void *h)
{
	window *w = h;
	if (!w || w->destroyed)
		return 0;
	w->destroyed = 1;
	if (dpy) {
		X.UnmapWindow(dpy, w->xwin);
		X.DestroyWindow(dpy, w->xwin);
		X.Flush(dpy);
	}
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
	if (w && dpy && t) {
		snprintf(w->title, sizeof w->title, "%s", t);
		X.StoreName(dpy, w->xwin, t);
	}
	return 1;
}

/* X keysym to Windows virtual-key code, for the keys programs read. */
static int vk_of(XID ks)
{
	if (ks >= 'a' && ks <= 'z')
		return (int)(ks - 'a' + 0x41);
	if (ks >= 'A' && ks <= 'Z')
		return (int)ks;
	if (ks >= '0' && ks <= '9')
		return (int)ks;
	if (ks >= 0xFFBE && ks <= 0xFFC9) /* F1..F12 */
		return 0x70 + (int)(ks - 0xFFBE);
	switch (ks) {
	case 0x20: return 0x20;        /* space */
	case 0xFF1B: return 0x1B;      /* Escape */
	case 0xFF0D: return 0x0D;      /* Return */
	case 0xFF08: return 0x08;      /* Backspace */
	case 0xFF09: return 0x09;      /* Tab */
	case 0xFFE1: case 0xFFE2: return 0x10; /* Shift */
	case 0xFFE3: case 0xFFE4: return 0x11; /* Control */
	case 0xFFE9: case 0xFFEA: return 0x12; /* Alt */
	case 0xFF51: return 0x25;      /* Left */
	case 0xFF52: return 0x26;      /* Up */
	case 0xFF53: return 0x27;      /* Right */
	case 0xFF54: return 0x28;      /* Down */
	}
	return 0;
}

MS int __vyw_PeekMessageA(unsigned char *msg, void *hwnd, u32 min, u32 max, u32 remove)
{
	(void)hwnd; (void)min; (void)max; (void)remove;
	if (!dpy || !msg)
		return 0;
	/* A window that was closed turns into one WM_QUIT. */
	for (int i = 0; i < g_nwin; i++) {
		if (g_windows[i] && g_windows[i]->pendingQuit) {
			g_windows[i]->pendingQuit = 0;
			memset(msg, 0, 48);
			*(u32 *)(msg + MSG_MESSAGE) = 0x0012; /* WM_QUIT */
			return 1;
		}
	}
	if (!X.Pending(dpy))
		return 0;
	XEvent e;
	X.NextEvent(dpy, &e);
	memset(msg, 0, 48);
	window *w = find_window(e.xany.window);
	switch (e.type) {
	case MotionNotify:
		*(u32 *)(msg + MSG_MESSAGE) = 0x0200; /* WM_MOUSEMOVE */
		*(long *)(msg + MSG_LPARAM) = (e.xmotion.x & 0xFFFF) | ((long)(e.xmotion.y & 0xFFFF) << 16);
		return 1;
	case ButtonPress:
	case ButtonRelease: {
		int press = e.type == ButtonPress;
		int b = e.xbutton.button;
		if (b == 1) {
			*(u32 *)(msg + MSG_MESSAGE) = press ? 0x0201 : 0x0202;
			g_async[1] = press ? 0x80 : 0; /* VK_LBUTTON */
		} else if (b == 3) {
			*(u32 *)(msg + MSG_MESSAGE) = press ? 0x0204 : 0x0205;
			g_async[2] = press ? 0x80 : 0; /* VK_RBUTTON */
		} else {
			*(u32 *)(msg + MSG_MESSAGE) = 0x0200;
		}
		*(long *)(msg + MSG_LPARAM) = (e.xbutton.x & 0xFFFF) | ((long)(e.xbutton.y & 0xFFFF) << 16);
		return 1;
	}
	case KeyPress:
	case KeyRelease: {
		int vk = vk_of(X.LookupKeysym(&e.xkey, 0));
		if (vk) {
			g_async[vk & 0xFF] = e.type == KeyPress ? 0x80 : 0;
			*(u32 *)(msg + MSG_MESSAGE) = e.type == KeyPress ? 0x0100 : 0x0101;
			*(long *)(msg + MSG_WPARAM) = vk;
		}
		return 1;
	}
	case ConfigureNotify:
		/* The back buffer is a fixed size; a resized frame just shows it
		 * unscaled, so there is nothing to rebuild here. */
		(void)w;
		return 1;
	case ClientMessage:
		if (w && (XID)e.xclient.data[0] == g_wmDelete)
			w->pendingQuit = 1;
		return 1;
	case DestroyNotify:
		if (w)
			w->pendingQuit = 1;
		return 1;
	}
	return 1; /* some other event; a real message may follow, so report one */
}

MS int __vyw_TranslateMessage(void *msg) { (void)msg; return 0; }
MS long __vyw_DispatchMessageA(void *msg) { (void)msg; return 0; }

MS int __vyw_GetAsyncKeyState(int vk)
{
	if (dpy) {
		/* drain so the state is current even without a poll this frame */
		while (X.Pending(dpy)) {
			unsigned char m[48];
			__vyw_PeekMessageA(m, NULL, 0, 0, 1);
		}
	}
	return (vk >= 0 && vk < 256 && g_async[vk]) ? 0x8000 : 0;
}

MS int __vyw_GetSystemMetrics(int i)
{
	if (!x11_init())
		return i == 0 ? 1920 : 1080;
	return i == 0 ? X.DisplayWidth(dpy, X.screen) : X.DisplayHeight(dpy, X.screen);
}

MS int __vyw_GetCursorPos(i32 *pt)
{
	if (!dpy || !pt)
		return 0;
	Window r, c;
	int rx, ry, wx, wy;
	unsigned mask;
	X.QueryPointer(dpy, X.root, &r, &c, &rx, &ry, &wx, &wy, &mask);
	pt[0] = rx;
	pt[1] = ry;
	return 1;
}

MS int __vyw_SetCursorPos(int x, int y)
{
	if (!dpy)
		return 0;
	X.WarpPointer(dpy, 0, X.root, 0, 0, 0, 0, x, y);
	X.Flush(dpy);
	return 1;
}

MS int __vyw_ShowCursor(int show)
{
	g_cursorCount += show ? 1 : -1;
	/* Hiding the cursor properly needs an invisible pixmap cursor; the
	 * count is tracked so the program's logic stays right even where the
	 * server shows one anyway. */
	return g_cursorCount;
}

MS void *__vyw_GetActiveWindow(void)
{
	if (!dpy)
		return NULL;
	Window f;
	int revert;
	X.GetInputFocus(dpy, &f, &revert);
	window *w = find_window(f);
	return w;
}

MS void *__vyw_FindWindowA(const char *cls, const char *title)
{
	(void)cls;
	if (!title)
		return NULL;
	for (int i = 0; i < g_nwin; i++)
		if (g_windows[i] && !strcmp(g_windows[i]->title, title))
			return g_windows[i];
	return NULL;
}

MS long __vyw_GetWindowLongPtrA(void *h, int idx)
{
	window *w = h;
	(void)idx;
	return w ? w->style : 0;
}

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
	(void)after; (void)flags;
	window *w = h;
	if (w && dpy && cx > 0 && cy > 0) {
		X.MoveResizeWindow(dpy, w->xwin, x, y, (unsigned)cx, (unsigned)cy);
		X.Flush(dpy);
	}
	return 1;
}

MS int __vyw_GetWindowRect(void *h, i32 *rect)
{
	window *w = h;
	if (!rect || !w)
		return 0;
	rect[0] = 0;
	rect[1] = 0;
	rect[2] = w->w;
	rect[3] = w->h;
	return 1;
}

/* EnumWindows calls back for each of our windows, Windows-convention. */
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
	snprintf(so, sizeof so, "%s", name);
	void *h = dlopen(so, RTLD_NOW | RTLD_GLOBAL);
	if (!h) {
		snprintf(so, sizeof so, "lib%s.so", name);
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

/* ---- sound, through PulseAudio ----------------------------------- */

#define MIX_RATE 11025
#define VOICES 24

typedef struct voice {
	int16_t *data;
	int len, pos;
	int loop, active;
	int volume; /* 0..256 */
	char alias[32];
} voice;

static voice g_voices[VOICES];
static pthread_mutex_t g_snd = PTHREAD_MUTEX_INITIALIZER;
static int g_mixerUp;

struct pa {
	void *lib;
	void *(*simple_new)(const char *server, const char *name, int dir, const char *dev,
			    const char *stream, const void *spec, const void *map,
			    const void *attr, int *err);
	int (*simple_write)(void *s, const void *data, size_t bytes, int *err);
	void (*simple_free)(void *s);
};
static struct pa PA;

static int16_t *load_wav(const char *path, int *outLen)
{
	/* Paths reach here with Windows separators, since os.path.join writes
	 * them; turn them back into Linux ones. */
	char fixed[4096];
	int k = 0;
	for (const char *p = path; p && *p && k < 4095; p++)
		fixed[k++] = *p == '\\' ? '/' : *p;
	fixed[k] = 0;
	FILE *f = fopen(fixed, "rb");
	if (!f)
		return NULL;
	unsigned char h[12];
	if (fread(h, 1, 12, f) != 12 || memcmp(h, "RIFF", 4) || memcmp(h + 8, "WAVE", 4)) {
		fclose(f);
		return NULL;
	}
	int channels = 1, bits = 8;
	long rate = MIX_RATE;
	int16_t *out = NULL;
	int n = 0;
	for (;;) {
		unsigned char c[8];
		if (fread(c, 1, 8, f) != 8)
			break;
		u32 sz = c[4] | c[5] << 8 | c[6] << 16 | (u32)c[7] << 24;
		if (!memcmp(c, "fmt ", 4)) {
			unsigned char fmt[16];
			u32 take = sz < 16 ? sz : 16;
			if (fread(fmt, 1, take, f) != take)
				break;
			channels = fmt[2] | fmt[3] << 8;
			rate = fmt[4] | fmt[5] << 8 | fmt[6] << 16 | (u32)fmt[7] << 24;
			bits = fmt[14] | fmt[15] << 8;
			if (sz > take)
				fseek(f, (long)(sz - take), SEEK_CUR);
		} else if (!memcmp(c, "data", 4)) {
			int frameBytes = (bits / 8) * (channels < 1 ? 1 : channels);
			if (frameBytes < 1)
				frameBytes = 1;
			int frames = (int)(sz / frameBytes);
			out = malloc(sizeof(int16_t) * (frames > 0 ? frames : 1));
			unsigned char fr[8];
			for (int i = 0; i < frames; i++) {
				if (fread(fr, 1, frameBytes, f) != (size_t)frameBytes)
					break;
				int s;
				if (bits == 8)
					s = ((int)fr[0] - 128) << 8;
				else
					s = (int16_t)(fr[0] | fr[1] << 8);
				out[n++] = (int16_t)s;
			}
			break;
		} else {
			fseek(f, (long)sz, SEEK_CUR);
		}
	}
	fclose(f);
	(void)rate;
	if (outLen)
		*outLen = n;
	return out;
}

static void *mixer_thread(void *arg)
{
	(void)arg;
	int err;
	/* PA_SAMPLE_S16LE = 3; spec is {format(int), rate(uint32), channels(uint8)} */
	struct {
		int format;
		u32 rate;
		uint8_t channels;
	} spec = {3, MIX_RATE, 1};
	void *s = PA.simple_new(NULL, "veyl", 1 /*PLAYBACK*/, NULL, "game", &spec, NULL, NULL, &err);
	if (!s)
		return NULL;
	int16_t buf[512];
	for (;;) {
		pthread_mutex_lock(&g_snd);
		for (int i = 0; i < 512; i++) {
			int acc = 0;
			for (int v = 0; v < VOICES; v++) {
				voice *vo = &g_voices[v];
				if (!vo->active || !vo->data)
					continue;
				acc += vo->data[vo->pos] * vo->volume / 256;
				if (++vo->pos >= vo->len) {
					if (vo->loop)
						vo->pos = 0;
					else
						vo->active = 0;
				}
			}
			if (acc > 32767) acc = 32767;
			if (acc < -32768) acc = -32768;
			buf[i] = (int16_t)acc;
		}
		pthread_mutex_unlock(&g_snd);
		if (PA.simple_write(s, buf, sizeof buf, &err) < 0)
			break;
	}
	PA.simple_free(s);
	return NULL;
}

static int audio_init(void)
{
	if (g_mixerUp)
		return 1;
	PA.lib = dlopen("libpulse-simple.so.0", RTLD_NOW);
	if (!PA.lib)
		PA.lib = dlopen("libpulse-simple.so", RTLD_NOW);
	if (!PA.lib)
		return 0;
	PA.simple_new = dlsym(PA.lib, "pa_simple_new");
	PA.simple_write = dlsym(PA.lib, "pa_simple_write");
	PA.simple_free = dlsym(PA.lib, "pa_simple_free");
	if (!PA.simple_new || !PA.simple_write || !PA.simple_free)
		return 0;
	pthread_t t;
	if (pthread_create(&t, NULL, mixer_thread, NULL) != 0)
		return 0;
	pthread_detach(t);
	g_mixerUp = 1;
	return 1;
}

static void voice_start(const char *alias, int16_t *data, int len, int loop, int volume)
{
	pthread_mutex_lock(&g_snd);
	int slot = -1;
	for (int v = 0; v < VOICES; v++) {
		if (alias && g_voices[v].active && !strcmp(g_voices[v].alias, alias)) {
			slot = v;
			break;
		}
	}
	if (slot < 0)
		for (int v = 0; v < VOICES; v++)
			if (!g_voices[v].active) {
				slot = v;
				break;
			}
	if (slot < 0)
		slot = 0;
	voice *vo = &g_voices[slot];
	if (data) {
		vo->data = data; /* kept; small and few, not freed */
		vo->len = len;
	}
	vo->pos = 0;
	vo->loop = loop;
	vo->volume = volume;
	vo->active = data || vo->data ? 1 : vo->active;
	if (alias)
		snprintf(vo->alias, sizeof vo->alias, "%s", alias);
	else
		vo->alias[0] = 0;
	pthread_mutex_unlock(&g_snd);
}

MS int __vyw_PlaySoundA(const char *path, void *mod, u32 flags)
{
	(void)mod;
	if (!path) { /* stop sound effects */
		pthread_mutex_lock(&g_snd);
		for (int v = 0; v < VOICES; v++)
			if (!g_voices[v].alias[0])
				g_voices[v].active = 0;
		pthread_mutex_unlock(&g_snd);
		return 1;
	}
	if (!audio_init())
		return 1; /* no audio server: silently succeed */
	int len = 0;
	int16_t *d = load_wav(path, &len);
	if (!d)
		return 0;
	voice_start(NULL, d, len, (flags & 0x0008) != 0, 256);
	return 1;
}

static voice *alias_voice(const char *name, int create)
{
	for (int v = 0; v < VOICES; v++)
		if (g_voices[v].alias[0] && !strcmp(g_voices[v].alias, name))
			return &g_voices[v];
	if (!create)
		return NULL;
	for (int v = 0; v < VOICES; v++)
		if (!g_voices[v].active && !g_voices[v].alias[0]) {
			snprintf(g_voices[v].alias, sizeof g_voices[v].alias, "%s", name);
			return &g_voices[v];
		}
	return NULL;
}

/* mciSendStringA: the small command set the music uses - open/alias,
 * setaudio volume, seek to start, play [repeat], stop, close. */
MS int __vyw_mciSendStringA(const char *cmd, char *ret, int retLen, void *cb)
{
	(void)ret; (void)retLen; (void)cb;
	if (!cmd)
		return 0;
	char buf[1024];
	snprintf(buf, sizeof buf, "%s", cmd);
	char *tok = strtok(buf, " ");
	if (!tok)
		return 0;

	if (!strcmp(tok, "open")) {
		char *rest = strtok(NULL, "");
		if (!rest)
			return 0;
		char path[768] = {0}, alias[64] = {0};
		char *q = strchr(rest, '"');
		if (q) {
			char *q2 = strchr(q + 1, '"');
			if (q2) {
				int n = (int)(q2 - q - 1);
				if (n > 767) n = 767;
				memcpy(path, q + 1, n);
			}
		} else {
			sscanf(rest, "%767s", path);
		}
		char *a = strstr(rest, "alias ");
		if (a)
			sscanf(a + 6, "%63s", alias);
		if (!alias[0])
			return 0;
		if (!audio_init())
			return 0;
		int len = 0;
		int16_t *d = load_wav(path, &len);
		if (!d)
			return 0;
		pthread_mutex_lock(&g_snd);
		voice *vo = alias_voice(alias, 1);
		if (vo) {
			vo->data = d;
			vo->len = len;
			vo->pos = 0;
			vo->active = 0;
			vo->volume = 256;
		}
		pthread_mutex_unlock(&g_snd);
		return 0;
	}

	char *name = strtok(NULL, " ");
	if (!name)
		return 0;
	pthread_mutex_lock(&g_snd);
	voice *vo = alias_voice(name, 0);
	int rc = 0;
	if (vo) {
		if (!strcmp(tok, "play")) {
			char *opt = strtok(NULL, " ");
			vo->loop = opt && !strcmp(opt, "repeat");
			vo->active = 1;
		} else if (!strcmp(tok, "stop")) {
			vo->active = 0;
		} else if (!strcmp(tok, "seek")) {
			vo->pos = 0;
		} else if (!strcmp(tok, "close")) {
			vo->active = 0;
			vo->alias[0] = 0;
		} else if (!strcmp(tok, "setaudio")) {
			char *to = strstr(cmd, "volume to ");
			if (to) {
				int vol = atoi(to + 10);
				vo->volume = vol * 256 / 1000;
			}
		}
	}
	pthread_mutex_unlock(&g_snd);
	return rc;
}
