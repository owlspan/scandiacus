/* Fixture for the static-linking golden test (tests/staticlib.vl).
 *
 * Rebuild the archive beside it with the MinGW cross tools:
 *   x86_64-w64-mingw32-gcc -c -O2 -D__USE_MINGW_ANSI_STDIO=0 \
 *       staticlib_src.c -o staticlib.o
 *   x86_64-w64-mingw32-ar rcs staticlib.a staticlib.o
 *
 * The archive is committed so the test runs without any C toolchain; the
 * source is committed so it can be rebuilt. It is deliberately
 * self-contained: it calls nothing outside itself, which is the case the
 * linker supports so far. It exercises a leaf function, a call between two
 * of its own functions, a read from .rdata, and a write to .data.
 */

int vy_add(int a, int b) { return a + b; }

__attribute__((noinline)) static int vy_square(int x) { return x * x; }
int vy_sumsq(int x) { return vy_square(x) + vy_square(x + 1); }

static const int table[5] = {2, 3, 5, 7, 11};
int vy_prime(int i) { return table[i]; }

static int counter = 100;
int vy_bump(void) { counter += 1; return counter; }
