/* Fixture for the static-linking golden test tests/staticdll.vl, the case
 * where a static library's own code calls into a DLL (here msvcrt, the C
 * runtime). Rebuild the archive beside it with:
 *   x86_64-w64-mingw32-gcc -c -O2 -D__USE_MINGW_ANSI_STDIO=0 \
 *       staticdll_src.c -o staticdll.o
 *   x86_64-w64-mingw32-ar rcs staticdll.a staticdll.o
 * The archive is committed so the test needs no C toolchain.
 */

#include <stdio.h>
#include <string.h>

int shout(const char *s) { return printf("[%s]\n", s); }
int lenof(const char *s) { return (int)strlen(s); }
