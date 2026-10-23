package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHTTPS fetches over TLS from Go servers whose certificates come
// from a CA made here, handed to the program's OpenSSL through
// SSL_CERT_FILE. It checks that a good certificate is accepted under a
// name and under an address, that a wrong name and an unknown CA are
// both refused with the reason, that a chunked body and a redirect work
// over TLS, and that a redirect down to http:// is refused.
//
// Linux only: on Windows https:// is WinHTTP's, which trusts the system
// store and not a file, and macOS is not run here.
func TestHTTPS(t *testing.T) {
	if !targetLinux() {
		t.Skip("https off Windows is tested on the Linux target")
	}
	for _, lib := range []string{"libssl.so.3", "libssl.so.1.1", "libssl.so"} {
		if out, _ := exec.Command("sh", "-c", "ldconfig -p | grep -q "+lib+" && echo y").Output(); strings.TrimSpace(string(out)) == "y" {
			goto found
		}
	}
	t.Skip("no libssl on this machine")
found:
	scandiacus := asmBackend(t)
	dir := t.TempDir()

	ca, caKey := makeCA(t)
	good := leafCert(t, ca, caKey, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	wrong := leafCert(t, ca, caKey, []string{"elsewhere.test"}, nil)
	otherCA, otherKey := makeCA(t)
	stranger := leafCert(t, otherCA, otherKey, []string{"localhost"}, nil)

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "plain")
	}))
	defer plain.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello over tls, %s", r.Method)
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "part%d ", i)
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		fmt.Fprintf(w, "posted %s", b[:n])
	})
	mux.HandleFunc("/move", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hello", http.StatusFound)
	})
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 200000))
	})
	serve := func(c tls.Certificate) *httptest.Server {
		s := httptest.NewUnstartedServer(mux)
		// The refused handshakes are the point of two of the cases.
		s.Config.ErrorLog = log.New(io.Discard, "", 0)
		s.TLS = &tls.Config{Certificates: []tls.Certificate{c}}
		s.StartTLS()
		return s
	}
	sGood, sWrong, sStranger := serve(good), serve(wrong), serve(stranger)
	defer sGood.Close()
	defer sWrong.Close()
	defer sStranger.Close()
	port := func(s *httptest.Server) string { return s.URL[strings.LastIndex(s.URL, ":")+1:] }

	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}

	src := fmt.Sprintf(`fn show(r: str!) {
    if isOk(r) {
        print(must(r))
    } else {
        print("error: " + errorOf(r))
    }
}
show(http.get("https://localhost:%[1]s/hello"))
show(http.get("https://127.0.0.1:%[1]s/hello"))
show(http.post("https://localhost:%[1]s/echo", "a=1"))
show(http.get("https://localhost:%[1]s/chunked"))
show(http.get("https://localhost:%[1]s/move"))
show(http.get("https://localhost:%[1]s/down"))
print(len(must(http.get("https://localhost:%[1]s/big"))))
show(http.get("https://localhost:%[2]s/hello"))
show(http.get("https://localhost:%[3]s/hello"))
`, port(sGood), port(sWrong), port(sStranger))
	p := filepath.Join(dir, "tls.scd")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(scandiacus, "run", p)
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caFile, "SSL_CERT_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	want := []string{
		"hello over tls, GET",
		"hello over tls, GET",
		"posted a=1",
		"part0 part1 part2 ",
		"hello over tls, GET",
		"error: refused a redirect from https:// to \"" + plain.URL + "/\", which is not encrypted",
		"200000",
		"error: cannot fetch \"https://localhost/hello\": the certificate of localhost was not accepted: hostname mismatch",
		"error: cannot fetch \"https://localhost/hello\": the certificate of localhost was not accepted: unable to get local issuer certificate",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), out)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, lines[i], want[i])
		}
	}
}

func makeCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "scandiacus test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

func leafCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string, ips []net.IP) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "scandiacus test server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
