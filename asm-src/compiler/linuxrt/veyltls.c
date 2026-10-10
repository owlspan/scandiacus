/*
 * veyltls.c - TLS for the http client on Linux and macOS.
 *
 * On Windows https:// goes through WinHTTP. Here the http client is the
 * socket one written in Veyl (__vy_httpOverNet), and this file wraps an
 * already connected socket in a TLS session with the system's OpenSSL,
 * so the client sends and receives through these instead of send/recv.
 *
 * OpenSSL is loaded with dlopen on first use rather than linked. A
 * program that never fetches an https:// url then needs no libssl on
 * the machine it runs on, and the build needs no OpenSSL headers or a
 * macOS SDK - the same reason the macOS sqlite and Cocoa layers dlopen.
 * Only the stable functions of the OpenSSL 1.1 and 3 ABI are used; the
 * macros (SSL_set_tlsext_host_name and friends) are spelled out as the
 * SSL_ctrl calls they expand to.
 *
 * Certificates are verified against the system's trust store, and the
 * name in the certificate must match the host asked for. A failure says
 * which check failed, in OpenSSL's words.
 */

#include <sys/socket.h>
#include <arpa/inet.h>
#include <dlfcn.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>

#define MS __attribute__((ms_abi))
typedef long long i64;

/* The constants are from ssl.h and x509_vfy.h; they are ABI. */
#define VERIFY_PEER 1
#define CTRL_SET_TLSEXT_HOSTNAME 55
#define TLSEXT_NAMETYPE_host_name 0
#define CTRL_SET_MIN_PROTO_VERSION 123
#define TLS1_2_VERSION 0x0303
#define OP_IGNORE_UNEXPECTED_EOF (1UL << 7)
#define ERROR_ZERO_RETURN 6
#define ERROR_SYSCALL 5
#define ERROR_SSL 1
#define X509_V_OK 0

static struct {
	int ok;
	void *ctx;
	const void *(*client_method)(void);
	void *(*ctx_new)(const void *);
	int (*ctx_default_paths)(void *);
	void (*ctx_set_verify)(void *, int, void *);
	unsigned long (*ctx_set_options)(void *, unsigned long);
	long (*ctx_ctrl)(void *, int, long, void *);
	void *(*ssl_new)(void *);
	int (*set_fd)(void *, int);
	long (*ctrl)(void *, int, long, void *);
	int (*set1_host)(void *, const char *);
	void *(*get0_param)(void *);
	int (*param_set1_ip_asc)(void *, const char *);
	int (*connect)(void *);
	int (*read)(void *, void *, int);
	int (*write)(void *, const void *, int);
	int (*shutdown)(void *);
	void (*ssl_free)(void *);
	int (*get_error)(const void *, int);
	long (*verify_result)(const void *);
	const char *(*verify_string)(long);
	unsigned long (*err_get)(void);
	void (*err_string)(unsigned long, char *, size_t);
	void (*err_clear)(void);
} T;

static pthread_once_t once = PTHREAD_ONCE_INIT;
static __thread char why[512];

static void *open_ssl(void)
{
	static const char *names[] = {
#ifdef __APPLE__
		/* Homebrew, Intel then Apple Silicon (an x86-64 program on
		 * Apple Silicon runs under Rosetta and cannot load an arm64
		 * library, but the path is tried in case it is universal).
		 * The unversioned /usr/lib/libssl.dylib is deliberately not
		 * tried: macOS aborts a process that loads it. */
		"/usr/local/opt/openssl@3/lib/libssl.3.dylib",
		"/usr/local/lib/libssl.3.dylib",
		"/opt/homebrew/opt/openssl@3/lib/libssl.3.dylib",
		"/usr/local/opt/openssl@1.1/lib/libssl.1.1.dylib",
		"/opt/local/lib/libssl.3.dylib",
#else
		"libssl.so.3",
		"libssl.so.1.1",
		"libssl.so",
#endif
		0,
	};
	for (int i = 0; names[i]; i++) {
		void *h = dlopen(names[i], RTLD_NOW | RTLD_GLOBAL);
		if (h)
			return h;
	}
	return 0;
}

static void load(void)
{
	void *h = open_ssl();
	if (!h) {
#ifdef __APPLE__
		snprintf(why, sizeof why, "HTTPS needs OpenSSL, which was not found - install it with: brew install openssl@3");
#else
		snprintf(why, sizeof why, "HTTPS needs OpenSSL (libssl), which was not found - install the libssl3 package");
#endif
		return;
	}
	/* dlsym on the libssl handle also searches libcrypto, which libssl
	 * depends on, so the ERR_ and X509_ functions resolve through it. */
	T.client_method = dlsym(h, "TLS_client_method");
	T.ctx_new = dlsym(h, "SSL_CTX_new");
	T.ctx_default_paths = dlsym(h, "SSL_CTX_set_default_verify_paths");
	T.ctx_set_verify = dlsym(h, "SSL_CTX_set_verify");
	T.ctx_set_options = dlsym(h, "SSL_CTX_set_options");
	T.ctx_ctrl = dlsym(h, "SSL_CTX_ctrl");
	T.ssl_new = dlsym(h, "SSL_new");
	T.set_fd = dlsym(h, "SSL_set_fd");
	T.ctrl = dlsym(h, "SSL_ctrl");
	T.set1_host = dlsym(h, "SSL_set1_host");
	T.get0_param = dlsym(h, "SSL_get0_param");
	T.param_set1_ip_asc = dlsym(h, "X509_VERIFY_PARAM_set1_ip_asc");
	T.connect = dlsym(h, "SSL_connect");
	T.read = dlsym(h, "SSL_read");
	T.write = dlsym(h, "SSL_write");
	T.shutdown = dlsym(h, "SSL_shutdown");
	T.ssl_free = dlsym(h, "SSL_free");
	T.get_error = dlsym(h, "SSL_get_error");
	T.verify_result = dlsym(h, "SSL_get_verify_result");
	T.verify_string = dlsym(h, "X509_verify_cert_error_string");
	T.err_get = dlsym(h, "ERR_get_error");
	T.err_string = dlsym(h, "ERR_error_string_n");
	T.err_clear = dlsym(h, "ERR_clear_error");
	if (!T.client_method || !T.ctx_new || !T.ctx_default_paths || !T.ctx_set_verify ||
	    !T.ssl_new || !T.set_fd || !T.ctrl || !T.set1_host || !T.get0_param ||
	    !T.param_set1_ip_asc || !T.connect || !T.read || !T.write || !T.shutdown ||
	    !T.ssl_free || !T.get_error || !T.verify_result || !T.verify_string ||
	    !T.err_get || !T.err_string || !T.err_clear) {
		snprintf(why, sizeof why, "the OpenSSL found is missing functions this needs (OpenSSL 1.1 or newer is required)");
		return;
	}

	T.ctx = T.ctx_new(T.client_method());
	if (!T.ctx) {
		snprintf(why, sizeof why, "OpenSSL could not create a TLS context");
		return;
	}
	T.ctx_set_verify(T.ctx, VERIFY_PEER, 0);
	T.ctx_default_paths(T.ctx);
	T.ctx_ctrl(T.ctx, CTRL_SET_MIN_PROTO_VERSION, TLS1_2_VERSION, 0);
	/* A server that closes the connection without a close_notify alert
	 * is common, and with Connection: close it marks the end of the
	 * body; OpenSSL 3 would otherwise report it as an error. */
	if (T.ctx_set_options)
		T.ctx_set_options(T.ctx, OP_IGNORE_UNEXPECTED_EOF);
	/* SSL_write writes to the socket with write(), which raises SIGPIPE
	 * when the server has gone, and that would end the program rather
	 * than fail the call. */
	signal(SIGPIPE, SIG_IGN);
	T.ok = 1;
}

/* The reason the last call on this thread failed. */
MS const char *__vyw_veyl_tls_error(void) { return why; }

static void ssl_reason(const char *what)
{
	unsigned long e = T.err_get();
	if (e) {
		char buf[256];
		T.err_string(e, buf, sizeof buf);
		snprintf(why, sizeof why, "%s: %s", what, buf);
	} else {
		snprintf(why, sizeof why, "%s", what);
	}
}

/* Wrap a connected socket. Returns the session, or 0 with the reason in
 * __vyw_veyl_tls_error. The socket stays the caller's to close. */
MS i64 __vyw_veyl_tls_open(i64 sock, const char *host)
{
	pthread_once(&once, load);
	if (!T.ok)
		return 0;
	T.err_clear();
	void *s = T.ssl_new(T.ctx);
	if (!s) {
		ssl_reason("TLS");
		return 0;
	}
	T.set_fd(s, (int)sock);

	/* An address is checked against the certificate's IP entries and
	 * sent without SNI, which is for names only. */
	unsigned char addr[16];
	if (inet_pton(AF_INET, host, addr) == 1 || inet_pton(AF_INET6, host, addr) == 1) {
		T.param_set1_ip_asc(T.get0_param(s), host);
	} else {
		T.ctrl(s, CTRL_SET_TLSEXT_HOSTNAME, TLSEXT_NAMETYPE_host_name, (void *)host);
		T.set1_host(s, host);
	}

	if (T.connect(s) != 1) {
		long v = T.verify_result(s);
		if (v != X509_V_OK)
			snprintf(why, sizeof why, "the certificate of %s was not accepted: %s", host, T.verify_string(v));
		else
			ssl_reason("TLS handshake failed");
		T.ssl_free(s);
		return 0;
	}
	return (i64)s;
}

/* Like send: how many bytes were written, or -1. */
MS i64 __vyw_veyl_tls_send(i64 s, const char *buf, i64 n)
{
	T.err_clear();
	int w = T.write((void *)s, buf, (int)n);
	if (w <= 0) {
		ssl_reason("TLS send");
		return -1;
	}
	return w;
}

/* Like recv: bytes read, 0 at the end of the stream, or -1. */
MS i64 __vyw_veyl_tls_recv(i64 s, char *buf, i64 cap)
{
	T.err_clear();
	int r = T.read((void *)s, buf, (int)cap);
	if (r > 0)
		return r;
	int e = T.get_error((void *)s, r);
	if (e == ERROR_ZERO_RETURN || (e == ERROR_SYSCALL && T.err_get() == 0))
		return 0;
	ssl_reason("TLS recv");
	return -1;
}

/* End the session. The socket is closed separately, with net.close. */
MS void __vyw_veyl_tls_close(i64 s)
{
	if (!s)
		return;
	T.shutdown((void *)s);
	T.ssl_free((void *)s);
}
