/*
 * veylsqlite.c - the db library's sqlite3 calls, on Linux and macOS.
 *
 * On Windows the db library loads sqlite3.dll, which the package ships. On
 * Linux and macOS the system sqlite is linked instead (-lsqlite3): this
 * file wraps each sqlite3_ function the library calls as an ms_abi
 * function that forwards to the real one, so the generated code reaches it
 * the same way it reaches any other runtime call. It is only compiled and
 * linked when a program actually uses db, so a program that does not need
 * sqlite installed to build.
 */

#define MS __attribute__((ms_abi))
typedef long long i64;

/* The real sqlite3 entry points, declared just enough to forward. The
 * handles (sqlite3*, sqlite3_stmt*) are opaque, so void* throughout. */
extern int sqlite3_open_v2(const char *, void **, int, const char *);
extern int sqlite3_close(void *);
extern const char *sqlite3_errmsg(void *);
extern int sqlite3_prepare_v2(void *, const char *, int, void **, const char **);
extern int sqlite3_bind_text(void *, int, const char *, int, void *);
extern int sqlite3_step(void *);
extern int sqlite3_column_count(void *);
extern const unsigned char *sqlite3_column_text(void *, int);
extern int sqlite3_finalize(void *);
extern int sqlite3_changes(void *);
extern i64 sqlite3_last_insert_rowid(void *);
extern const char *sqlite3_libversion(void);

MS int __vyw_sqlite3_open_v2(const char *name, void **db, int flags, const char *vfs)
{
	return sqlite3_open_v2(name, db, flags, vfs);
}
MS int __vyw_sqlite3_close(void *db) { return sqlite3_close(db); }
MS const char *__vyw_sqlite3_errmsg(void *db) { return sqlite3_errmsg(db); }
MS int __vyw_sqlite3_prepare_v2(void *db, const char *sql, int n, void **stmt, const char **tail)
{
	return sqlite3_prepare_v2(db, sql, n, stmt, tail);
}
MS int __vyw_sqlite3_bind_text(void *stmt, int i, const char *s, int n, void *destructor)
{
	return sqlite3_bind_text(stmt, i, s, n, destructor);
}
MS int __vyw_sqlite3_step(void *stmt) { return sqlite3_step(stmt); }
MS int __vyw_sqlite3_column_count(void *stmt) { return sqlite3_column_count(stmt); }
MS const unsigned char *__vyw_sqlite3_column_text(void *stmt, int col)
{
	return sqlite3_column_text(stmt, col);
}
MS int __vyw_sqlite3_finalize(void *stmt) { return sqlite3_finalize(stmt); }
MS int __vyw_sqlite3_changes(void *db) { return sqlite3_changes(db); }
MS i64 __vyw_sqlite3_last_insert_rowid(void *db) { return sqlite3_last_insert_rowid(db); }
MS const char *__vyw_sqlite3_libversion(void) { return sqlite3_libversion(); }
