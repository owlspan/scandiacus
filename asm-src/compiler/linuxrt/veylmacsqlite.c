/*
 * veylmacsqlite.c - the db library's sqlite3 calls, on macOS.
 *
 * The Linux build links the system libsqlite3 directly (see veylsqlite.c).
 * macOS ships libsqlite3.dylib with the OS, but the compiler cross-builds
 * for macOS with a toolchain that has no macOS SDK to link against, so the
 * library is loaded with dlopen at run time instead - the same way the
 * Cocoa window layer loads AppKit. Every sqlite3_ function the db library
 * calls is answered by an ms_abi wrapper here that resolves the real one
 * on first use and forwards to it.
 *
 * It is only compiled and linked when a program actually uses db, so a
 * program that does not touch db carries no dependency on sqlite at all.
 */

#include <dlfcn.h>
#include <stdlib.h>

#define MS __attribute__((ms_abi))
typedef long long i64;

static struct sq {
	int ok;
	int (*open_v2)(const char *, void **, int, const char *);
	int (*close)(void *);
	const char *(*errmsg)(void *);
	int (*prepare_v2)(void *, const char *, int, void **, const char **);
	int (*bind_text)(void *, int, const char *, int, void *);
	int (*step)(void *);
	int (*column_count)(void *);
	const unsigned char *(*column_text)(void *, int);
	int (*finalize)(void *);
	int (*changes)(void *);
	i64 (*last_insert_rowid)(void *);
	const char *(*libversion)(void);
} SQ;

static int sq_load(void)
{
	if (SQ.ok)
		return 1;
	void *h = dlopen("libsqlite3.dylib", RTLD_NOW | RTLD_GLOBAL);
	if (!h)
		h = dlopen("/usr/lib/libsqlite3.dylib", RTLD_NOW | RTLD_GLOBAL);
	if (!h)
		return 0;
	SQ.open_v2 = dlsym(h, "sqlite3_open_v2");
	SQ.close = dlsym(h, "sqlite3_close");
	SQ.errmsg = dlsym(h, "sqlite3_errmsg");
	SQ.prepare_v2 = dlsym(h, "sqlite3_prepare_v2");
	SQ.bind_text = dlsym(h, "sqlite3_bind_text");
	SQ.step = dlsym(h, "sqlite3_step");
	SQ.column_count = dlsym(h, "sqlite3_column_count");
	SQ.column_text = dlsym(h, "sqlite3_column_text");
	SQ.finalize = dlsym(h, "sqlite3_finalize");
	SQ.changes = dlsym(h, "sqlite3_changes");
	SQ.last_insert_rowid = dlsym(h, "sqlite3_last_insert_rowid");
	SQ.libversion = dlsym(h, "sqlite3_libversion");
	SQ.ok = SQ.open_v2 && SQ.prepare_v2 && SQ.step;
	return SQ.ok;
}

MS int __vyw_sqlite3_open_v2(const char *name, void **db, int flags, const char *vfs)
{
	if (!sq_load())
		return 1; /* SQLITE_ERROR */
	return SQ.open_v2(name, db, flags, vfs);
}
MS int __vyw_sqlite3_close(void *db) { return sq_load() ? SQ.close(db) : 0; }
MS const char *__vyw_sqlite3_errmsg(void *db)
{
	return sq_load() ? SQ.errmsg(db) : "sqlite is not available";
}
MS int __vyw_sqlite3_prepare_v2(void *db, const char *sql, int n, void **stmt, const char **tail)
{
	return sq_load() ? SQ.prepare_v2(db, sql, n, stmt, tail) : 1;
}
MS int __vyw_sqlite3_bind_text(void *stmt, int i, const char *s, int n, void *destructor)
{
	return sq_load() ? SQ.bind_text(stmt, i, s, n, destructor) : 1;
}
MS int __vyw_sqlite3_step(void *stmt) { return sq_load() ? SQ.step(stmt) : 1; }
MS int __vyw_sqlite3_column_count(void *stmt)
{
	return sq_load() ? SQ.column_count(stmt) : 0;
}
MS const unsigned char *__vyw_sqlite3_column_text(void *stmt, int col)
{
	return sq_load() ? SQ.column_text(stmt, col) : 0;
}
MS int __vyw_sqlite3_finalize(void *stmt) { return sq_load() ? SQ.finalize(stmt) : 0; }
MS int __vyw_sqlite3_changes(void *db) { return sq_load() ? SQ.changes(db) : 0; }
MS i64 __vyw_sqlite3_last_insert_rowid(void *db)
{
	return sq_load() ? SQ.last_insert_rowid(db) : 0;
}
MS const char *__vyw_sqlite3_libversion(void)
{
	return sq_load() ? SQ.libversion() : "0";
}
