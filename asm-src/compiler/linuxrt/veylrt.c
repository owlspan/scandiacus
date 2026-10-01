/*
 * veylrt.c - the runtime a Veyl program links against on Linux.
 *
 * The compiler's code generator targets the Windows x64 calling
 * convention and calls the C runtime (msvcrt) and kernel32 by name. On
 * Linux neither library exists, so this file provides each function the
 * generated code can call, under the name __vyw_<name>, implemented on
 * POSIX and glibc. The build renames every import in the program object
 * to that prefix (see linux.go), so the program's code is the same bytes
 * on both systems and only this layer differs.
 *
 * Every function here is declared ms_abi: GCC then receives arguments
 * the Windows way (rcx, rdx, r8, r9, then the stack past 32 bytes of
 * shadow space) and preserves the registers Windows says a callee must
 * (rdi, rsi, xmm6-xmm15), which System V code would otherwise clobber.
 * Function pointers the program hands over - a thread entry, a qsort
 * comparator, the crash handler - are Windows-convention code too, and
 * are called through ms_abi pointer types.
 *
 * Only the functions a console program needs are here. Windows-only
 * libraries (windows and drawing, sound, COM, sockets, HTTP, process
 * inspection) are not, and linux.go turns a program that needs one into
 * a build error naming the call rather than a link failure.
 *
 * Plain ASCII, like the rest of the repository. Built by linux.go with
 * the system C compiler and cached; nothing here is exported except the
 * __vyw_ names and main.
 */

#define _GNU_SOURCE
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <fnmatch.h>
#include <limits.h>
#include <math.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/uio.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <ucontext.h>
#include <unistd.h>

#define MS __attribute__((ms_abi))
#define VYW(name) MS __vyw_##name

typedef long long i64;
typedef unsigned long long u64;
typedef unsigned int u32;

/* ---- start-up ---------------------------------------------------- */

/* __start is the program's own entry point: it aligns the stack, runs
 * main and hands its result to exit. It is Windows-convention code. */
extern MS void __start(void);

static int g_argc;
static char **g_argv;
static char *g_cmdline;

static void install_alt_stack(void);

int main(int argc, char **argv)
{
	g_argc = argc;
	g_argv = argv;
	install_alt_stack();
	__start();
	return 0;
}

/* ---- the last-error value ---------------------------------------- */

static __thread u32 g_last_error;

/* win_error maps errno to the Win32 error code the program expects. */
static u32 win_error(int e)
{
	switch (e) {
	case ENOENT: return 2;          /* ERROR_FILE_NOT_FOUND */
	case ENOTDIR: return 3;         /* ERROR_PATH_NOT_FOUND */
	case EACCES: case EPERM: case EROFS: return 5; /* ERROR_ACCESS_DENIED */
	case EBADF: return 6;           /* ERROR_INVALID_HANDLE */
	case ENOMEM: return 8;          /* ERROR_NOT_ENOUGH_MEMORY */
	case EEXIST: return 183;        /* ERROR_ALREADY_EXISTS */
	case ENOTEMPTY: return 145;     /* ERROR_DIR_NOT_EMPTY */
	case EISDIR: return 5;
	case EINVAL: return 87;         /* ERROR_INVALID_PARAMETER */
	case EBUSY: return 32;          /* ERROR_SHARING_VIOLATION */
	case ENOSPC: return 112;        /* ERROR_DISK_FULL */
	case ENAMETOOLONG: return 206;  /* ERROR_FILENAME_EXCED_RANGE */
	case EXDEV: return 17;          /* ERROR_NOT_SAME_DEVICE */
	}
	return 31; /* ERROR_GEN_FAILURE */
}

static void set_errno_error(void) { g_last_error = win_error(errno); }

u32 VYW(GetLastError)(void) { return g_last_error; }

/* The system's own wording for the codes above, the same sentences
 * Windows uses, so a program's error text reads the same on both. */
static const char *win_message(u32 code)
{
	switch (code) {
	case 2: return "The system cannot find the file specified.";
	case 3: return "The system cannot find the path specified.";
	case 5: return "Access is denied.";
	case 6: return "The handle is invalid.";
	case 8: return "Not enough memory resources are available to process this command.";
	case 17: return "The system cannot move the file to a different disk drive.";
	case 18: return "There are no more files.";
	case 32: return "The process cannot access the file because it is being used by another process.";
	case 80: return "The file exists.";
	case 87: return "The parameter is incorrect.";
	case 112: return "There is not enough space on the disk.";
	case 145: return "The directory is not empty.";
	case 183: return "Cannot create a file when that file already exists.";
	case 206: return "The filename or extension is too long.";
	case 267: return "The directory name is invalid.";
	}
	return "A device attached to the system is not functioning.";
}

/* FormatMessageA, for the FROM_SYSTEM case the runtime uses: the text
 * goes into the caller's buffer with the trailing CR LF Windows adds. */
u32 VYW(FormatMessageA)(u32 flags, void *src, u32 code, u32 lang, char *buf, u32 size, void *args)
{
	(void)flags; (void)src; (void)lang; (void)args;
	char tmp[256];
	int n = snprintf(tmp, sizeof tmp, "%s\r\n", win_message(code));
	if (!buf || size == 0)
		return 0;
	if ((u32)n >= size)
		n = (int)size - 1;
	memcpy(buf, tmp, n);
	buf[n] = 0;
	return (u32)n;
}

/* ---- printf, the Windows way ------------------------------------- */

/* The program passes printf arguments by the Windows variadic rules, so
 * they have to be read from a Windows va_list, and glibc's printf cannot
 * read one. fmt_ms walks the format and formats each conversion on its
 * own with snprintf, fetching each argument as the conversion says.
 * Windows spelling is taken into account: %ld is 32 bits there, and
 * %I64d is %lld. */

struct sbuf {
	char *p;
	size_t n, cap;
};

static void sb_put(struct sbuf *b, const char *s, size_t n)
{
	if (b->n + n + 1 > b->cap) {
		size_t c = b->cap ? b->cap * 2 : 256;
		while (c < b->n + n + 1)
			c *= 2;
		b->p = realloc(b->p, c);
		b->cap = c;
	}
	memcpy(b->p + b->n, s, n);
	b->n += n;
	b->p[b->n] = 0;
}

static void sb_fmt(struct sbuf *b, const char *spec, ...)
{
	va_list ap, ap2;
	va_start(ap, spec);
	va_copy(ap2, ap);
	int n = vsnprintf(NULL, 0, spec, ap);
	va_end(ap);
	if (n > 0) {
		char *tmp = malloc((size_t)n + 1);
		vsnprintf(tmp, (size_t)n + 1, spec, ap2);
		sb_put(b, tmp, (size_t)n);
		free(tmp);
	}
	va_end(ap2);
}

static void fmt_ms(struct sbuf *b, const char *f, __builtin_ms_va_list *ap)
{
	sb_put(b, "", 0);
	while (*f) {
		if (*f != '%') {
			const char *q = f;
			while (*q && *q != '%')
				q++;
			sb_put(b, f, (size_t)(q - f));
			f = q;
			continue;
		}
		if (f[1] == '%') {
			sb_put(b, "%", 1);
			f += 2;
			continue;
		}
		char spec[96];
		int k = 0;
		spec[k++] = *f++;
		while (*f && strchr("-+ #0'", *f) && k < 40)
			spec[k++] = *f++;
		if (*f == '*') {
			k += snprintf(spec + k, sizeof spec - k, "%d", __builtin_va_arg(*ap, int));
			f++;
		} else {
			while (isdigit((unsigned char)*f) && k < 60)
				spec[k++] = *f++;
		}
		if (*f == '.') {
			spec[k++] = *f++;
			if (*f == '*') {
				k += snprintf(spec + k, sizeof spec - k, "%d", __builtin_va_arg(*ap, int));
				f++;
			} else {
				while (isdigit((unsigned char)*f) && k < 80)
					spec[k++] = *f++;
			}
		}
		int wide = 0; /* a 64-bit integer argument */
		if (f[0] == 'l' && f[1] == 'l') {
			wide = 1;
			f += 2;
		} else if (f[0] == 'I' && f[1] == '6' && f[2] == '4') {
			wide = 1;
			f += 3;
		} else if (f[0] == 'I' && f[1] == '3' && f[2] == '2') {
			f += 3;
		} else if (*f == 'I' || *f == 'z' || *f == 'j' || *f == 't') {
			wide = 1;
			f++;
		} else if (*f == 'l' || *f == 'L') {
			f++; /* long is 32 bits on Windows; L only matters for floats */
		} else if (*f == 'h') {
			f++;
			if (*f == 'h')
				f++;
		}
		char c = *f;
		if (!c)
			break;
		f++;
		switch (c) {
		case 'd': case 'i':
			if (wide) {
				spec[k++] = 'l'; spec[k++] = 'l'; spec[k++] = c; spec[k] = 0;
				sb_fmt(b, spec, __builtin_va_arg(*ap, i64));
			} else {
				spec[k++] = c; spec[k] = 0;
				sb_fmt(b, spec, (int)__builtin_va_arg(*ap, i64));
			}
			break;
		case 'u': case 'x': case 'X': case 'o':
			if (wide) {
				spec[k++] = 'l'; spec[k++] = 'l'; spec[k++] = c; spec[k] = 0;
				sb_fmt(b, spec, __builtin_va_arg(*ap, u64));
			} else {
				spec[k++] = c; spec[k] = 0;
				sb_fmt(b, spec, (unsigned)__builtin_va_arg(*ap, u64));
			}
			break;
		case 'c':
			spec[k++] = c; spec[k] = 0;
			sb_fmt(b, spec, (int)__builtin_va_arg(*ap, i64));
			break;
		case 's': {
			const char *s = __builtin_va_arg(*ap, const char *);
			spec[k++] = c; spec[k] = 0;
			sb_fmt(b, spec, s ? s : "(null)");
			break;
		}
		case 'p':
			spec[k++] = c; spec[k] = 0;
			sb_fmt(b, spec, __builtin_va_arg(*ap, void *));
			break;
		case 'e': case 'E': case 'g': case 'G': {
			/* msvcrt writes at least three exponent digits, 1e+007, and
			 * the program's float printing is written against that. */
			spec[k++] = c; spec[k] = 0;
			size_t at = b->n;
			sb_fmt(b, spec, __builtin_va_arg(*ap, double));
			for (size_t i = at; i + 1 < b->n; i++) {
				if ((b->p[i] == 'e' || b->p[i] == 'E') && (b->p[i + 1] == '+' || b->p[i + 1] == '-')) {
					size_t d = i + 2, e = d;
					while (e < b->n && isdigit((unsigned char)b->p[e]))
						e++;
					size_t digits = e - d;
					if (digits >= 1 && digits < 3) {
						size_t pad = 3 - digits;
						sb_put(b, "000", pad); /* grow, then shift the tail */
						memmove(b->p + d + pad, b->p + d, b->n - pad - d);
						memset(b->p + d, '0', pad);
					}
					break;
				}
			}
			break;
		}
		case 'f': case 'F': case 'a': case 'A':
			spec[k++] = c; spec[k] = 0;
			sb_fmt(b, spec, __builtin_va_arg(*ap, double));
			break;
		case 'n':
			(void)__builtin_va_arg(*ap, void *);
			break;
		default:
			spec[k++] = c; spec[k] = 0;
			sb_put(b, spec, (size_t)k);
			break;
		}
	}
}

int VYW(printf)(const char *fmt, ...)
{
	struct sbuf b = {0};
	__builtin_ms_va_list ap;
	__builtin_ms_va_start(ap, fmt);
	fmt_ms(&b, fmt, &ap);
	__builtin_ms_va_end(ap);
	fwrite(b.p, 1, b.n, stdout);
	int n = (int)b.n;
	free(b.p);
	return n;
}

/* msvcrt's _snprintf: a result that does not fit is truncated with no
 * terminator and the call returns -1; one that fits exactly has no
 * terminator either and returns its length. */
static int snprintf_ms(char *dst, size_t size, const char *fmt, __builtin_ms_va_list *ap, int c99)
{
	struct sbuf b = {0};
	fmt_ms(&b, fmt, ap);
	int n = (int)b.n;
	if (c99) {
		if (size) {
			size_t m = b.n < size - 1 ? b.n : size - 1;
			memcpy(dst, b.p, m);
			dst[m] = 0;
		}
	} else if (b.n < size) {
		memcpy(dst, b.p, b.n + 1);
	} else {
		memcpy(dst, b.p, size);
		if (b.n > size)
			n = -1;
	}
	free(b.p);
	return n;
}

int VYW(_snprintf)(char *dst, size_t size, const char *fmt, ...)
{
	__builtin_ms_va_list ap;
	__builtin_ms_va_start(ap, fmt);
	int n = snprintf_ms(dst, size, fmt, &ap, 0);
	__builtin_ms_va_end(ap);
	return n;
}

int VYW(snprintf)(char *dst, size_t size, const char *fmt, ...)
{
	__builtin_ms_va_list ap;
	__builtin_ms_va_start(ap, fmt);
	int n = snprintf_ms(dst, size, fmt, &ap, 1);
	__builtin_ms_va_end(ap);
	return n;
}

int VYW(sprintf)(char *dst, const char *fmt, ...)
{
	__builtin_ms_va_list ap;
	__builtin_ms_va_start(ap, fmt);
	int n = snprintf_ms(dst, (size_t)INT_MAX, fmt, &ap, 1);
	__builtin_ms_va_end(ap);
	return n;
}

int VYW(puts)(const char *s) { return puts(s); }
int VYW(putchar)(int c) { return putchar(c); }

/* ---- process ----------------------------------------------------- */

void VYW(exit)(int code)
{
	fflush(NULL);
	exit(code);
}

void VYW(ExitProcess)(u32 code)
{
	fflush(NULL);
	_exit((int)code);
}

int VYW(fflush)(FILE *f) { return fflush(f); }
int VYW(_write)(int fd, const void *buf, u32 n) { return (int)write(fd, buf, n); }
int VYW(_setmode)(int fd, int mode) { (void)fd; (void)mode; return 0; }
int VYW(_isatty)(int fd) { return isatty(fd); }
/* Windows always sets TEMP and TMP, and programs written there read
 * them for a scratch directory; Linux spells it TMPDIR, or leaves it
 * unset and means /tmp. */
char * VYW(getenv)(const char *name)
{
	char *v = getenv(name);
	if (!v && (!strcmp(name, "TEMP") || !strcmp(name, "TMP"))) {
		v = getenv("TMPDIR");
		if (!v || !*v)
			v = "/tmp";
	}
	return v;
}

int VYW(_putenv)(const char *s)
{
	/* putenv keeps the pointer, so it gets a copy of its own. */
	return putenv(strdup(s));
}

static char *fix_mode(const char *mode, char *out)
{
	int k = 0;
	for (; *mode && k < 7; mode++)
		if (*mode != 'b' && *mode != 't')
			out[k++] = *mode;
	out[k] = 0;
	return out;
}

FILE * VYW(_popen)(const char *cmd, const char *mode)
{
	char m[8];
	fflush(NULL);
	return popen(cmd, fix_mode(mode, m));
}

int VYW(_pclose)(FILE *f)
{
	int st = pclose(f);
	if (st != -1 && WIFEXITED(st))
		return WEXITSTATUS(st);
	return st;
}

char * VYW(fgets)(char *buf, int n, FILE *f) { return fgets(buf, n, f); }
u32 VYW(GetCurrentProcessId)(void) { return (u32)getpid(); }
u32 VYW(GetCurrentThreadId)(void) { return (u32)gettid(); }
u32 VYW(GetActiveProcessorCount)(unsigned short group) { (void)group; return (u32)sysconf(_SC_NPROCESSORS_ONLN); }
void VYW(Sleep)(u32 ms)
{
	struct timespec ts = {ms / 1000, (long)(ms % 1000) * 1000000L};
	while (nanosleep(&ts, &ts) == -1 && errno == EINTR)
		;
}

int VYW(GetComputerNameA)(char *buf, u32 *size)
{
	char tmp[256];
	if (gethostname(tmp, sizeof tmp) != 0)
		return 0;
	tmp[sizeof tmp - 1] = 0;
	size_t n = strlen(tmp);
	if (n + 1 > *size) {
		*size = (u32)n + 1;
		g_last_error = 111; /* ERROR_BUFFER_OVERFLOW */
		return 0;
	}
	memcpy(buf, tmp, n + 1);
	*size = (u32)n;
	return 1;
}

/* GetCommandLineA: the program splits a Windows command line itself, so
 * argv is joined back into one with Windows quoting. */
static void quote_arg(struct sbuf *b, const char *a)
{
	if (*a && !strpbrk(a, " \t\n\v\"")) {
		sb_put(b, a, strlen(a));
		return;
	}
	sb_put(b, "\"", 1);
	for (;;) {
		size_t slashes = 0;
		while (*a == '\\') {
			a++;
			slashes++;
		}
		if (!*a) {
			for (size_t i = 0; i < slashes * 2; i++)
				sb_put(b, "\\", 1);
			break;
		}
		if (*a == '"') {
			for (size_t i = 0; i < slashes * 2 + 1; i++)
				sb_put(b, "\\", 1);
			sb_put(b, "\"", 1);
		} else {
			for (size_t i = 0; i < slashes; i++)
				sb_put(b, "\\", 1);
			sb_put(b, a, 1);
		}
		a++;
	}
	sb_put(b, "\"", 1);
}

char * VYW(GetCommandLineA)(void)
{
	if (!g_cmdline) {
		struct sbuf b = {0};
		sb_put(&b, "", 0);
		for (int i = 0; i < g_argc; i++) {
			if (i)
				sb_put(&b, " ", 1);
			quote_arg(&b, g_argv[i]);
		}
		g_cmdline = b.p;
	}
	return g_cmdline;
}

/* ---- memory ------------------------------------------------------ */

void * VYW(malloc)(size_t n) { return malloc(n); }
void * VYW(calloc)(size_t n, size_t s) { return calloc(n, s); }
void * VYW(realloc)(void *p, size_t n) { return realloc(p, n); }
void VYW(free)(void *p) { free(p); }
/* The process heap. On Windows it is a different heap from the one the
 * C runtime's malloc uses, and programs can rely on that: mem.alloc
 * takes from it, and mem.protect on such a block makes its page
 * read-only, which must not freeze malloc's own bookkeeping. So it is a
 * separate allocator here too, on pages malloc never touches: blocks of
 * power-of-two sizes carved from 1 MB regions, a free list per size, and
 * a mapping of its own for anything large. */

#define HEAP_MIN_SHIFT 4
#define HEAP_CLASSES 15 /* 16 bytes up to 256 KB */
#define HEAP_REGION ((size_t)1 << 20)
#define HEAP_HDR 16

static pthread_mutex_t heap_lock = PTHREAD_MUTEX_INITIALIZER;
static void *heap_free_list[HEAP_CLASSES];
static unsigned char *heap_bump, *heap_end;

/* A block's header holds its usable size; a large block's size is
 * marked by the low bit, since it has its own mapping. */
static size_t heap_size_of(void *p) { return *(size_t *)((unsigned char *)p - HEAP_HDR) & ~(size_t)1; }

static void *heap_alloc(size_t n, int zero)
{
	if (n == 0)
		n = 1;
	int c = 0;
	while (c < HEAP_CLASSES && ((size_t)1 << (c + HEAP_MIN_SHIFT)) < n)
		c++;
	if (c == HEAP_CLASSES) {
		size_t total = (n + HEAP_HDR + 4095) & ~(size_t)4095;
		unsigned char *m = mmap(NULL, total, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		if (m == MAP_FAILED)
			return NULL;
		*(size_t *)m = (total - HEAP_HDR) | 1;
		return m + HEAP_HDR; /* fresh pages are already zero */
	}
	size_t size = (size_t)1 << (c + HEAP_MIN_SHIFT);
	pthread_mutex_lock(&heap_lock);
	unsigned char *p = heap_free_list[c];
	if (p) {
		heap_free_list[c] = *(void **)p;
	} else {
		if (!heap_bump || heap_bump + HEAP_HDR + size > heap_end) {
			heap_bump = mmap(NULL, HEAP_REGION, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
			if (heap_bump == MAP_FAILED) {
				heap_bump = NULL;
				pthread_mutex_unlock(&heap_lock);
				return NULL;
			}
			heap_end = heap_bump + HEAP_REGION;
		}
		*(size_t *)heap_bump = size;
		p = heap_bump + HEAP_HDR;
		heap_bump += HEAP_HDR + size;
	}
	pthread_mutex_unlock(&heap_lock);
	if (zero)
		memset(p, 0, size);
	return p;
}

static void heap_free(void *p)
{
	if (!p)
		return;
	size_t raw = *(size_t *)((unsigned char *)p - HEAP_HDR);
	if (raw & 1) {
		munmap((unsigned char *)p - HEAP_HDR, (raw & ~(size_t)1) + HEAP_HDR);
		return;
	}
	int c = 0;
	while (((size_t)1 << (c + HEAP_MIN_SHIFT)) < raw)
		c++;
	pthread_mutex_lock(&heap_lock);
	*(void **)p = heap_free_list[c];
	heap_free_list[c] = p;
	pthread_mutex_unlock(&heap_lock);
}

void * VYW(GetProcessHeap)(void) { return (void *)1; }
void * VYW(HeapAlloc)(void *h, u32 flags, size_t n) { (void)h; return heap_alloc(n, (flags & 8) != 0); }
int VYW(HeapFree)(void *h, u32 flags, void *p) { (void)h; (void)flags; heap_free(p); return 1; }

void * VYW(HeapReAlloc)(void *h, u32 flags, void *p, size_t n)
{
	(void)h;
	if (!p)
		return heap_alloc(n, (flags & 8) != 0);
	size_t old = heap_size_of(p);
	if (n <= old)
		return p;
	void *q = heap_alloc(n, (flags & 8) != 0);
	if (!q)
		return NULL;
	memcpy(q, p, old);
	heap_free(p);
	return q;
}

int VYW(VirtualProtect)(void *addr, size_t size, u32 prot, u32 *old)
{
	int p = PROT_NONE;
	switch (prot & 0xff) {
	case 0x02: p = PROT_READ; break;
	case 0x04: p = PROT_READ | PROT_WRITE; break;
	case 0x10: p = PROT_EXEC; break;
	case 0x20: p = PROT_READ | PROT_EXEC; break;
	case 0x40: p = PROT_READ | PROT_WRITE | PROT_EXEC; break;
	}
	uintptr_t page = (uintptr_t)sysconf(_SC_PAGESIZE);
	uintptr_t start = (uintptr_t)addr & ~(page - 1);
	size_t len = (uintptr_t)addr + size - start;
	if (old)
		*old = 0x04;
	if (mprotect((void *)start, len, p) != 0) {
		set_errno_error();
		return 0;
	}
	return 1;
}

/* ---- strings and numbers ----------------------------------------- */

size_t VYW(strlen)(const char *s) { return strlen(s); }
char * VYW(strcpy)(char *d, const char *s) { return strcpy(d, s); }
char * VYW(strcat)(char *d, const char *s) { return strcat(d, s); }
int VYW(strcmp)(const char *a, const char *b) { return strcmp(a, b); }
int VYW(strncmp)(const char *a, const char *b, size_t n) { return strncmp(a, b, n); }
char * VYW(strchr)(const char *s, int c) { return strchr(s, c); }
char * VYW(strstr)(const char *a, const char *b) { return strstr(a, b); }
void * VYW(memcpy)(void *d, const void *s, size_t n) { return memcpy(d, s, n); }
void * VYW(memmove)(void *d, const void *s, size_t n) { return memmove(d, s, n); }
void * VYW(memset)(void *d, int c, size_t n) { return memset(d, c, n); }
int VYW(memcmp)(const void *a, const void *b, size_t n) { return memcmp(a, b, n); }
void * VYW(memchr)(const void *s, int c, size_t n) { return memchr(s, c, n); }
int VYW(toupper)(int c) { return toupper(c); }
int VYW(tolower)(int c) { return tolower(c); }
int VYW(atoi)(const char *s) { return atoi(s); }
double VYW(strtod)(const char *s, char **end) { return strtod(s, end); }
/* long is 32 bits on Windows, and the program reads the result as such. */
int VYW(strtol)(const char *s, char **end, int base) { return (int)strtol(s, end, base); }
unsigned VYW(strtoul)(const char *s, char **end, int base) { return (unsigned)strtoul(s, end, base); }
i64 VYW(_strtoi64)(const char *s, char **end, int base) { return strtoll(s, end, base); }
u64 VYW(_strtoui64)(const char *s, char **end, int base) { return strtoull(s, end, base); }
unsigned VYW(_rotl)(unsigned v, int s) { s &= 31; return (v << s) | (v >> ((32 - s) & 31)); }

typedef MS int (*ms_cmp)(const void *, const void *);
static __thread ms_cmp g_cmp;
static int call_cmp(const void *a, const void *b) { return g_cmp(a, b); }

void VYW(qsort)(void *base, size_t n, size_t size, ms_cmp cmp)
{
	ms_cmp saved = g_cmp;
	g_cmp = cmp;
	qsort(base, n, size, call_cmp);
	g_cmp = saved;
}

/* ---- maths ------------------------------------------------------- */

double VYW(sqrt)(double x) { return sqrt(x); }
float VYW(sqrtf)(float x) { return sqrtf(x); }
double VYW(pow)(double x, double y) { return pow(x, y); }
float VYW(powf)(float x, float y) { return powf(x, y); }
double VYW(fmod)(double x, double y) { return fmod(x, y); }
float VYW(fmodf)(float x, float y) { return fmodf(x, y); }
double VYW(modf)(double x, double *i) { return modf(x, i); }
float VYW(modff)(float x, float *i) { return modff(x, i); }
double VYW(frexp)(double x, int *e) { return frexp(x, e); }
double VYW(ldexp)(double x, int e) { return ldexp(x, e); }
double VYW(floor)(double x) { return floor(x); }
double VYW(ceil)(double x) { return ceil(x); }
double VYW(round)(double x) { return round(x); }
double VYW(trunc)(double x) { return trunc(x); }
double VYW(fabs)(double x) { return fabs(x); }
double VYW(sin)(double x) { return sin(x); }
double VYW(cos)(double x) { return cos(x); }
double VYW(tan)(double x) { return tan(x); }
double VYW(asin)(double x) { return asin(x); }
double VYW(acos)(double x) { return acos(x); }
double VYW(atan)(double x) { return atan(x); }
double VYW(atan2)(double y, double x) { return atan2(y, x); }
double VYW(sinh)(double x) { return sinh(x); }
double VYW(cosh)(double x) { return cosh(x); }
double VYW(tanh)(double x) { return tanh(x); }
double VYW(exp)(double x) { return exp(x); }
double VYW(log)(double x) { return log(x); }
double VYW(log2)(double x) { return log2(x); }
double VYW(log10)(double x) { return log10(x); }
double VYW(hypot)(double x, double y) { return hypot(x, y); }
float VYW(sinf)(float x) { return sinf(x); }
float VYW(cosf)(float x) { return cosf(x); }

/* ---- time -------------------------------------------------------- */

i64 VYW(time)(i64 *t)
{
	i64 now = (i64)time(NULL);
	if (t)
		*t = now;
	return now;
}

/* FILETIME counts 100 ns ticks from 1601. */
void VYW(GetSystemTimeAsFileTime)(u64 *ft)
{
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	*ft = (u64)ts.tv_sec * 10000000ULL + (u64)ts.tv_nsec / 100 + 116444736000000000ULL;
}

/* struct tm is nine ints on both systems; glibc's adds fields after
 * them, which the program never reads. */
struct tm * VYW(_localtime64)(const i64 *t)
{
	static __thread struct tm out;
	time_t tt = (time_t)*t;
	return localtime_r(&tt, &out) ? &out : NULL;
}

i64 VYW(_mktime64)(int *wtm)
{
	struct tm tm = {0};
	tm.tm_sec = wtm[0];
	tm.tm_min = wtm[1];
	tm.tm_hour = wtm[2];
	tm.tm_mday = wtm[3];
	tm.tm_mon = wtm[4];
	tm.tm_year = wtm[5];
	tm.tm_isdst = wtm[8];
	i64 r = (i64)mktime(&tm);
	wtm[6] = tm.tm_wday;
	wtm[7] = tm.tm_yday;
	return r;
}

/* ---- handles ----------------------------------------------------- */

/* A Windows HANDLE here is a pointer to one of these. */
enum { H_FILE = 1, H_THREAD, H_FIND, H_PROC };

struct handle {
	int kind;
	int fd;
	pthread_t thread;
	int joined;
	/* a directory search */
	char **names;
	int count, next;
	char *dir;
};

#define INVALID_HANDLE ((void *)(intptr_t)-1)

static struct handle std_handles[3] = {
	{H_FILE, 0, 0, 0, 0, 0, 0, 0},
	{H_FILE, 1, 0, 0, 0, 0, 0, 0},
	{H_FILE, 2, 0, 0, 0, 0, 0, 0},
};

void * VYW(GetStdHandle)(u32 which)
{
	switch ((int)which) {
	case -10: return &std_handles[0];
	case -11: return &std_handles[1];
	case -12: return &std_handles[2];
	}
	return INVALID_HANDLE;
}

/* fix_path turns backslashes into slashes, so a path written for
 * Windows still names the same file. */
static char *fix_path(const char *p)
{
	char *s = strdup(p ? p : "");
	for (char *c = s; *c; c++)
		if (*c == '\\')
			*c = '/';
	return s;
}

void * VYW(CreateFileA)(const char *name, u32 access, u32 share, void *sa, u32 disp, u32 flags, void *tmpl)
{
	(void)share; (void)sa; (void)flags; (void)tmpl;
	int o = 0;
	int r = (access & 0x80000000u) || (access & 0x10000000u);
	int w = (access & 0x40000000u) || (access & 0x10000000u) || (access & 4);
	o = (r && w) ? O_RDWR : w ? O_WRONLY : O_RDONLY;
	if (access & 4) /* FILE_APPEND_DATA */
		o |= O_APPEND;
	switch (disp) {
	case 1: o |= O_CREAT | O_EXCL; break; /* CREATE_NEW */
	case 2: o |= O_CREAT | O_TRUNC; break; /* CREATE_ALWAYS */
	case 3: break; /* OPEN_EXISTING */
	case 4: o |= O_CREAT; break; /* OPEN_ALWAYS */
	case 5: o |= O_TRUNC; break; /* TRUNCATE_EXISTING */
	}
	char *path = fix_path(name);
	int fd = open(path, o | O_CLOEXEC, 0666);
	if (fd >= 0) {
		struct stat st;
		if (fstat(fd, &st) == 0 && S_ISDIR(st.st_mode) && w) {
			close(fd);
			fd = -1;
			errno = EISDIR;
		}
	}
	free(path);
	if (fd < 0) {
		set_errno_error();
		return INVALID_HANDLE;
	}
	struct handle *h = calloc(1, sizeof *h);
	h->kind = H_FILE;
	h->fd = fd;
	g_last_error = 0;
	return h;
}

int VYW(ReadFile)(struct handle *h, void *buf, u32 n, u32 *got, void *ovl)
{
	(void)ovl;
	ssize_t r;
	do
		r = read(h->fd, buf, n);
	while (r < 0 && errno == EINTR);
	if (r < 0) {
		set_errno_error();
		if (got)
			*got = 0;
		return 0;
	}
	if (got)
		*got = (u32)r;
	return 1;
}

int VYW(WriteFile)(struct handle *h, const void *buf, u32 n, u32 *put, void *ovl)
{
	(void)ovl;
	/* The program's printf output sits in stdio's buffer; a direct write
	 * to the same descriptor must not overtake it. */
	if (h->fd == 1 || h->fd == 2)
		fflush(h->fd == 1 ? stdout : stderr);
	size_t done = 0;
	while (done < n) {
		ssize_t r = write(h->fd, (const char *)buf + done, n - done);
		if (r < 0) {
			if (errno == EINTR)
				continue;
			set_errno_error();
			if (put)
				*put = (u32)done;
			return 0;
		}
		done += (size_t)r;
	}
	if (put)
		*put = (u32)done;
	return 1;
}

int VYW(GetFileSizeEx)(struct handle *h, i64 *size)
{
	struct stat st;
	if (fstat(h->fd, &st) != 0) {
		set_errno_error();
		return 0;
	}
	*size = (i64)st.st_size;
	return 1;
}

u32 VYW(SetFilePointer)(struct handle *h, int lo, int *hi, u32 method)
{
	i64 off = hi ? ((i64)*hi << 32) | (u32)lo : (i64)lo;
	int whence = method == 1 ? SEEK_CUR : method == 2 ? SEEK_END : SEEK_SET;
	off_t r = lseek(h->fd, (off_t)off, whence);
	if (r < 0) {
		set_errno_error();
		return 0xFFFFFFFFu;
	}
	if (hi)
		*hi = (int)((u64)r >> 32);
	return (u32)r;
}

static void free_find(struct handle *h)
{
	for (int i = 0; i < h->count; i++)
		free(h->names[i]);
	free(h->names);
	free(h->dir);
}

typedef MS u32 (*ms_thread_fn)(void *);

int VYW(CloseHandle)(struct handle *h)
{
	if (!h || h == INVALID_HANDLE)
		return 0;
	if (h >= &std_handles[0] && h <= &std_handles[2])
		return 1;
	switch (h->kind) {
	case H_FILE:
		close(h->fd);
		break;
	case H_THREAD:
		if (!h->joined)
			pthread_detach(h->thread);
		break;
	case H_FIND:
		free_find(h);
		break;
	}
	free(h);
	return 1;
}

/* ---- files and directories --------------------------------------- */

/* FILETIME from a time_t. */
static u64 filetime(time_t t) { return (u64)t * 10000000ULL + 116444736000000000ULL; }

static u32 attrs_of(const struct stat *st, const char *name)
{
	u32 a = S_ISDIR(st->st_mode) ? 0x10 : 0x20; /* DIRECTORY : ARCHIVE */
	if (!(st->st_mode & S_IWUSR))
		a |= 0x01; /* READONLY */
	const char *base = strrchr(name, '/');
	base = base ? base + 1 : name;
	if (base[0] == '.' && strcmp(base, ".") && strcmp(base, ".."))
		a |= 0x02; /* HIDDEN */
	return a;
}

u32 VYW(GetFileAttributesA)(const char *name)
{
	char *p = fix_path(name);
	struct stat st;
	int rc = stat(p, &st);
	u32 a = rc == 0 ? attrs_of(&st, p) : 0xFFFFFFFFu;
	if (rc != 0)
		set_errno_error();
	free(p);
	return a;
}

/* WIN32_FILE_ATTRIBUTE_DATA: attributes, three FILETIMEs, size high,
 * size low - 36 bytes, written field by field because the FILETIMEs
 * are only four-byte aligned. */
static void put_u32(unsigned char *p, u32 v) { memcpy(p, &v, 4); }
static void put_u64(unsigned char *p, u64 v) { memcpy(p, &v, 8); }

int VYW(GetFileAttributesExA)(const char *name, int level, unsigned char *data)
{
	(void)level;
	char *p = fix_path(name);
	struct stat st;
	int rc = stat(p, &st);
	if (rc != 0) {
		set_errno_error();
		free(p);
		return 0;
	}
	put_u32(data + 0, attrs_of(&st, p));
	put_u64(data + 4, filetime(st.st_ctime));
	put_u64(data + 12, filetime(st.st_atime));
	put_u64(data + 20, filetime(st.st_mtime));
	put_u32(data + 28, S_ISDIR(st.st_mode) ? 0 : (u32)((u64)st.st_size >> 32));
	put_u32(data + 32, S_ISDIR(st.st_mode) ? 0 : (u32)st.st_size);
	free(p);
	return 1;
}

int VYW(DeleteFileA)(const char *name)
{
	char *p = fix_path(name);
	struct stat st;
	int rc;
	if (stat(p, &st) == 0 && S_ISDIR(st.st_mode)) {
		errno = EACCES;
		rc = -1;
	} else {
		rc = unlink(p);
	}
	if (rc != 0)
		set_errno_error();
	free(p);
	return rc == 0;
}

int VYW(MoveFileExA)(const char *from, const char *to, u32 flags)
{
	char *a = fix_path(from), *b = fix_path(to);
	int rc = 0;
	struct stat st;
	if (!(flags & 1) && stat(b, &st) == 0) { /* no MOVEFILE_REPLACE_EXISTING */
		errno = EEXIST;
		rc = -1;
	} else {
		rc = rename(a, b);
	}
	if (rc != 0) {
		set_errno_error();
		if (errno == EEXIST)
			g_last_error = 183;
	}
	free(a);
	free(b);
	return rc == 0;
}

int VYW(CreateDirectoryA)(const char *name, void *sa)
{
	(void)sa;
	char *p = fix_path(name);
	int rc = mkdir(p, 0777);
	if (rc != 0)
		set_errno_error();
	free(p);
	return rc == 0;
}

int VYW(RemoveDirectoryA)(const char *name)
{
	char *p = fix_path(name);
	int rc = rmdir(p);
	if (rc != 0) {
		set_errno_error();
		if (errno == ENOTDIR)
			g_last_error = 267;
	}
	free(p);
	return rc == 0;
}

u32 VYW(GetCurrentDirectoryA)(u32 size, char *buf)
{
	char tmp[PATH_MAX];
	if (!getcwd(tmp, sizeof tmp)) {
		set_errno_error();
		return 0;
	}
	size_t n = strlen(tmp);
	if (n + 1 > size)
		return (u32)n + 1;
	memcpy(buf, tmp, n + 1);
	return (u32)n;
}

int VYW(SetCurrentDirectoryA)(const char *name)
{
	char *p = fix_path(name);
	int rc = chdir(p);
	if (rc != 0)
		set_errno_error();
	free(p);
	return rc == 0;
}

u32 VYW(GetFullPathNameA)(const char *name, u32 size, char *buf, char **file)
{
	char *p = fix_path(name);
	char tmp[PATH_MAX * 2];
	if (p[0] == '/') {
		snprintf(tmp, sizeof tmp, "%s", p);
	} else {
		char cwd[PATH_MAX];
		if (!getcwd(cwd, sizeof cwd))
			cwd[0] = 0;
		snprintf(tmp, sizeof tmp, "%s/%s", cwd, p);
	}
	free(p);
	/* Resolve . and .. by hand: the file need not exist. */
	char out[PATH_MAX * 2];
	size_t o = 0;
	out[0] = 0;
	for (char *seg = strtok(tmp, "/"); seg; seg = strtok(NULL, "/")) {
		if (!strcmp(seg, "."))
			continue;
		if (!strcmp(seg, "..")) {
			while (o > 0 && out[o - 1] != '/')
				o--;
			if (o > 0)
				o--;
			out[o] = 0;
			continue;
		}
		o += (size_t)snprintf(out + o, sizeof out - o, "/%s", seg);
	}
	if (o == 0) {
		out[0] = '/';
		out[1] = 0;
		o = 1;
	}
	if (o + 1 > size)
		return (u32)o + 1;
	memcpy(buf, out, o + 1);
	if (file) {
		char *slash = strrchr(buf, '/');
		*file = slash ? slash + 1 : buf;
	}
	return (u32)o;
}

/* FindFirstFileA / FindNextFileA over a "dir\*"-style pattern. The
 * names are read up front and sorted the way NTFS returns them. */
static int cmp_names(const void *a, const void *b)
{
	return strcasecmp(*(char *const *)a, *(char *const *)b);
}

/* WIN32_FIND_DATAA: attributes at 0, three FILETIMEs from 4, size high
 * at 28, size low at 32, two reserved words, the name at 44. */
static void fill_find(struct handle *h, const char *name, unsigned char *data)
{
	memset(data, 0, 320);
	char *full = malloc(strlen(h->dir) + strlen(name) + 2);
	sprintf(full, "%s/%s", h->dir, name);
	struct stat st;
	if (stat(full, &st) == 0) {
		put_u32(data + 0, attrs_of(&st, name));
		put_u64(data + 4, filetime(st.st_ctime));
		put_u64(data + 12, filetime(st.st_atime));
		put_u64(data + 20, filetime(st.st_mtime));
		if (!S_ISDIR(st.st_mode)) {
			put_u32(data + 28, (u32)((u64)st.st_size >> 32));
			put_u32(data + 32, (u32)st.st_size);
		}
	}
	free(full);
	snprintf((char *)data + 44, 260, "%s", name);
}

void * VYW(FindFirstFileA)(const char *pattern, unsigned char *data)
{
	char *p = fix_path(pattern);
	char *slash = strrchr(p, '/');
	const char *glob = slash ? slash + 1 : p;
	char *dir = slash ? strndup(p, (size_t)(slash - p)) : strdup(".");
	if (!*dir) {
		free(dir);
		dir = strdup("/");
	}
	DIR *d = opendir(dir);
	if (!d) {
		set_errno_error();
		if (errno == ENOENT)
			g_last_error = 3;
		free(dir);
		free(p);
		return INVALID_HANDLE;
	}
	struct handle *h = calloc(1, sizeof *h);
	h->kind = H_FIND;
	h->dir = dir;
	int cap = 0;
	for (struct dirent *e; (e = readdir(d));) {
		if (fnmatch(glob, e->d_name, 0) != 0)
			continue;
		if (h->count == cap) {
			cap = cap ? cap * 2 : 32;
			h->names = realloc(h->names, sizeof(char *) * (size_t)cap);
		}
		h->names[h->count++] = strdup(e->d_name);
	}
	closedir(d);
	free(p);
	qsort(h->names, (size_t)h->count, sizeof(char *), cmp_names);
	if (h->count == 0) {
		free_find(h);
		free(h);
		g_last_error = 2;
		return INVALID_HANDLE;
	}
	fill_find(h, h->names[0], data);
	h->next = 1;
	return h;
}

int VYW(FindNextFileA)(struct handle *h, unsigned char *data)
{
	if (h->next >= h->count) {
		g_last_error = 18; /* ERROR_NO_MORE_FILES */
		return 0;
	}
	fill_find(h, h->names[h->next++], data);
	return 1;
}

int VYW(FindClose)(struct handle *h) { return __vyw_CloseHandle(h); }

/* ---- the clock and this process's own memory --------------------- */

/* SYSTEMTIME: eight 16-bit fields - year, month, day-of-week, day, hour,
 * minute, second, millisecond. */
void VYW(GetSystemTime)(uint16_t *st)
{
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	struct tm tm;
	time_t t = ts.tv_sec;
	gmtime_r(&t, &tm);
	st[0] = (uint16_t)(tm.tm_year + 1900);
	st[1] = (uint16_t)(tm.tm_mon + 1);
	st[2] = (uint16_t)tm.tm_wday;
	st[3] = (uint16_t)tm.tm_mday;
	st[4] = (uint16_t)tm.tm_hour;
	st[5] = (uint16_t)tm.tm_min;
	st[6] = (uint16_t)tm.tm_sec;
	st[7] = (uint16_t)(ts.tv_nsec / 1000000);
}

u32 VYW(GetTickCount)(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (u32)(ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
}

/* A process handle. For the program's own process, reading its memory is
 * a plain copy; for another, process_vm_readv does the same across the
 * boundary where the system allows it. */
void *VYW(OpenProcess)(u32 access, int inherit, u32 pid)
{
	(void)access; (void)inherit;
	struct handle *h = calloc(1, sizeof *h);
	h->kind = H_PROC;
	h->fd = (int)pid;
	return h;
}

int VYW(ReadProcessMemory)(struct handle *h, const void *addr, void *buf, size_t n, size_t *got)
{
	if (!h || h->kind != H_PROC) {
		set_errno_error();
		return 0;
	}
	size_t done = 0;
	if (h->fd == (int)getpid()) {
		memcpy(buf, addr, n);
		done = n;
	} else {
		struct iovec local = {buf, n};
		struct iovec remote = {(void *)addr, n};
		ssize_t r = process_vm_readv(h->fd, &local, 1, &remote, 1, 0);
		if (r < 0) {
			set_errno_error();
			return 0;
		}
		done = (size_t)r;
	}
	if (got)
		*got = done;
	return done == n;
}

int VYW(WriteProcessMemory)(struct handle *h, void *addr, const void *buf, size_t n, size_t *put)
{
	if (!h || h->kind != H_PROC) {
		set_errno_error();
		return 0;
	}
	size_t done = 0;
	if (h->fd == (int)getpid()) {
		memcpy(addr, buf, n);
		done = n;
	} else {
		struct iovec local = {(void *)buf, n};
		struct iovec remote = {addr, n};
		ssize_t r = process_vm_writev(h->fd, &local, 1, &remote, 1, 0);
		if (r < 0) {
			set_errno_error();
			return 0;
		}
		done = (size_t)r;
	}
	if (put)
		*put = done;
	return done == n;
}

/* ---- threads ----------------------------------------------------- */

struct start {
	ms_thread_fn fn;
	void *arg;
};

static void *thread_main(void *p)
{
	struct start s = *(struct start *)p;
	free(p);
	install_alt_stack();
	s.fn(s.arg);
	return NULL;
}

void * VYW(CreateThread)(void *sa, size_t stack, ms_thread_fn fn, void *arg, u32 flags, u32 *id)
{
	(void)sa; (void)flags;
	struct start *s = malloc(sizeof *s);
	s->fn = fn;
	s->arg = arg;
	pthread_attr_t attr;
	pthread_attr_init(&attr);
	/* Windows gives a thread 1 MB unless told otherwise; the program's
	 * recursion depth was judged against that. */
	pthread_attr_setstacksize(&attr, stack ? stack : (size_t)8 << 20);
	struct handle *h = calloc(1, sizeof *h);
	h->kind = H_THREAD;
	if (pthread_create(&h->thread, &attr, thread_main, s) != 0) {
		pthread_attr_destroy(&attr);
		free(s);
		free(h);
		g_last_error = 8;
		return NULL;
	}
	pthread_attr_destroy(&attr);
	if (id)
		*id = 0;
	return h;
}

u32 VYW(WaitForSingleObject)(struct handle *h, u32 ms)
{
	(void)ms;
	if (h && h->kind == H_THREAD && !h->joined) {
		pthread_join(h->thread, NULL);
		h->joined = 1;
	}
	return 0; /* WAIT_OBJECT_0 */
}

/* A CRITICAL_SECTION is 40 bytes the program owns; the mutex is kept
 * outside it and its first word points there. Windows critical sections
 * are recursive, so the mutex is too. */
void VYW(InitializeCriticalSection)(void **cs)
{
	pthread_mutex_t *m = malloc(sizeof *m);
	pthread_mutexattr_t a;
	pthread_mutexattr_init(&a);
	pthread_mutexattr_settype(&a, PTHREAD_MUTEX_RECURSIVE);
	pthread_mutex_init(m, &a);
	pthread_mutexattr_destroy(&a);
	*cs = m;
}

void VYW(EnterCriticalSection)(void **cs) { pthread_mutex_lock((pthread_mutex_t *)*cs); }
void VYW(LeaveCriticalSection)(void **cs) { pthread_mutex_unlock((pthread_mutex_t *)*cs); }
void VYW(DeleteCriticalSection)(void **cs)
{
	pthread_mutex_destroy((pthread_mutex_t *)*cs);
	free(*cs);
}

/* A CONDITION_VARIABLE is one zeroed word; the condition is made on
 * first use and the word set to point at it, once, by whichever thread
 * gets there first. */
static pthread_cond_t *cond_of(void **cv)
{
	pthread_cond_t *c = __atomic_load_n((pthread_cond_t **)cv, __ATOMIC_ACQUIRE);
	if (c)
		return c;
	pthread_cond_t *n = malloc(sizeof *n);
	pthread_cond_init(n, NULL);
	pthread_cond_t *expected = NULL;
	if (__atomic_compare_exchange_n((pthread_cond_t **)cv, &expected, n, 0,
					__ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE))
		return n;
	pthread_cond_destroy(n);
	free(n);
	return expected;
}

int VYW(SleepConditionVariableCS)(void **cv, void **cs, u32 ms)
{
	(void)ms;
	pthread_cond_wait(cond_of(cv), (pthread_mutex_t *)*cs);
	return 1;
}

void VYW(WakeAllConditionVariable)(void **cv) { pthread_cond_broadcast(cond_of(cv)); }
void VYW(WakeConditionVariable)(void **cv) { pthread_cond_signal(cond_of(cv)); }

/* The bounds of the calling thread's stack, for the collector's scan. */
void VYW(GetCurrentThreadStackLimits)(uintptr_t *low, uintptr_t *high)
{
	pthread_attr_t a;
	void *addr = NULL;
	size_t size = 0;
	if (pthread_getattr_np(pthread_self(), &a) == 0) {
		pthread_attr_getstack(&a, &addr, &size);
		pthread_attr_destroy(&a);
	}
	*low = (uintptr_t)addr;
	*high = (uintptr_t)addr + size;
}

int VYW(SetThreadStackGuarantee)(u32 *size) { (void)size; return 1; }

/* ---- crashes ----------------------------------------------------- */

/* The program installs a Windows vectored exception handler. Here a
 * signal handler stands in: it builds the two records the handler reads
 * - the exception code, and a CONTEXT whose Rip is at 0xF8 - and calls
 * it. The handler reports the crash and ends the process itself; if it
 * declines, the signal's default action follows. */

struct exc_ptrs {
	void *record;
	void *context;
};
typedef MS long (*ms_handler)(struct exc_ptrs *);

static ms_handler g_handler;

/* A handler for a stack overflow cannot run on the stack that
 * overflowed, so every thread gets a small one of its own. */
static void install_alt_stack(void)
{
	stack_t ss;
	ss.ss_size = 64 * 1024;
	ss.ss_sp = malloc(ss.ss_size);
	ss.ss_flags = 0;
	if (ss.ss_sp)
		sigaltstack(&ss, NULL);
}

static void on_signal(int sig, siginfo_t *si, void *uc_)
{
	ucontext_t *uc = uc_;
	u32 code = 0xC0000005u; /* ACCESS_VIOLATION */
	if (sig == SIGFPE)
		code = si->si_code == FPE_INTOVF ? 0xC0000095u : 0xC0000094u;
	else if (sig == SIGILL)
		code = 0xC000001Du;
	else if (sig == SIGSEGV) {
		/* A fault just below the stack is the stack running out. */
		uintptr_t low = 0, high = 0;
		__vyw_GetCurrentThreadStackLimits(&low, &high);
		uintptr_t a = (uintptr_t)si->si_addr;
		if (low && a < low + 64 * 1024 && a + (1 << 20) >= low)
			code = 0xC00000FDu;
	}

	static __thread unsigned char record[152];
	static __thread unsigned char context[1232];
	memset(record, 0, sizeof record);
	memset(context, 0, sizeof context);
	memcpy(record, &code, 4);
	u64 rip = (u64)uc->uc_mcontext.gregs[REG_RIP];
	u64 rsp = (u64)uc->uc_mcontext.gregs[REG_RSP];
	memcpy(context + 0xF8, &rip, 8);
	memcpy(context + 0x98, &rsp, 8);

	struct exc_ptrs p = {record, context};
	if (g_handler && g_handler(&p) != 0)
		return;
	signal(sig, SIG_DFL);
}

void * VYW(AddVectoredExceptionHandler)(u32 first, ms_handler h)
{
	(void)first;
	g_handler = h;
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_sigaction = on_signal;
	sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
	sigemptyset(&sa.sa_mask);
	sigaction(SIGSEGV, &sa, NULL);
	sigaction(SIGBUS, &sa, NULL);
	sigaction(SIGFPE, &sa, NULL);
	sigaction(SIGILL, &sa, NULL);
	return (void *)h;
}
