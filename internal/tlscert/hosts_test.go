package tlscert

import (
	"crypto/x509"
	"slices"
	"testing"
	"time"
)

func leafHosts(t *testing.T, b *Bundle) []string {
	t.Helper()
	leaf, err := x509.ParseCertificate(b.Cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return normalize(certHosts(leaf))
}

// At boot the service can start before DHCP: the IP is missing from the
// hosts it sees. The certificate must keep covering the IP.
func TestBootBeforeDHCPKeepsIP(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	full := []string{"localhost", "127.0.0.1", "sprinklerpi", "sprinklerpi.local", "192.168.1.20"}
	noIP := full[:4]

	b1, err := Ensure(dir, full, now)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Ensure(dir, noIP, now.Add(time.Minute)) // reboot, no address yet
	if err != nil {
		t.Fatal(err)
	}
	if string(b2.Cert.Certificate[0]) != string(b1.Cert.Certificate[0]) {
		t.Fatal("a subset of the hosts must reuse the certificate")
	}
	if !slices.Contains(b2.Hosts, "192.168.1.20") {
		t.Fatalf("reported hosts lost the IP: %v", b2.Hosts)
	}

	// A new address (e.g. a new DHCP lease) is added; the old one stays.
	b3, err := Ensure(dir, append(noIP, "192.168.1.21"), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got := leafHosts(t, b3)
	for _, h := range []string{"192.168.1.20", "192.168.1.21", "sprinklerpi.local"} {
		if !slices.Contains(got, h) {
			t.Errorf("re-issued cert lacks %s: %v", h, got)
		}
	}

	// Renewal near expiry, while the IP is missing, keeps it too.
	b4, err := Ensure(dir, noIP, now.Add(790*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if string(b4.Cert.Certificate[0]) == string(b3.Cert.Certificate[0]) {
		t.Fatal("near expiry: should renew")
	}
	if got := leafHosts(t, b4); !slices.Contains(got, "192.168.1.20") || !slices.Contains(got, "192.168.1.21") {
		t.Fatalf("renewed cert dropped names: %v", got)
	}
	if string(b4.CAPEM) != string(b1.CAPEM) {
		t.Fatal("CA must be kept")
	}
}
