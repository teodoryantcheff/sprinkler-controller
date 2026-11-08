package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEnsureIssuesTrustedCertAndReuses(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	hosts := []string{"192.168.1.20", "sprinklerpi", "sprinklerpi.local", "127.0.0.1"}
	b, err := Ensure(dir, hosts, now)
	if err != nil {
		t.Fatal(err)
	}
	// A client that trusts only our CA accepts the server certificate.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b.CAPEM) {
		t.Fatal("CA PEM not parseable")
	}
	leaf, _ := x509.ParseCertificate(b.Cert.Certificate[0])
	for _, h := range []string{"192.168.1.20", "sprinklerpi.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: h, CurrentTime: now}); err != nil {
			t.Errorf("verify for %s: %v", h, err)
		}
	}
	if d := leaf.NotAfter.Sub(leaf.NotBefore); d > 825*24*time.Hour {
		t.Errorf("server cert valid %v: iOS rejects more than 825 days", d)
	}

	// Same hosts: reused. New host: re-issued with the same CA.
	b2, _ := Ensure(dir, hosts, now.Add(time.Hour))
	if string(b2.Cert.Certificate[0]) != string(b.Cert.Certificate[0]) {
		t.Error("certificate should be reused")
	}
	b3, _ := Ensure(dir, append(hosts, "10.0.0.5"), now)
	if string(b3.Cert.Certificate[0]) == string(b.Cert.Certificate[0]) || string(b3.CAPEM) != string(b.CAPEM) {
		t.Error("new host: new server cert, same CA")
	}
	// Near expiry: renewed.
	b4, _ := Ensure(dir, append(hosts, "10.0.0.5"), now.Add(790*24*time.Hour))
	if string(b4.Cert.Certificate[0]) == string(b3.Cert.Certificate[0]) {
		t.Error("certificate near expiry should be renewed")
	}
}

func TestServesHTTPS(t *testing.T) {
	b, err := Ensure(t.TempDir(), []string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{b.Cert}}
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b.CAPEM)
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
