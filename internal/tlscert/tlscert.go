// Package tlscert gives the controller HTTPS on a home network, where no
// public certificate authority can issue a certificate. It keeps a small
// local CA (10 years) and a server certificate signed by it for the
// controller's addresses. Install the CA once on each phone (it is served
// at /ca.crt) and HTTPS works without warnings.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"sprinkler/internal/fsutil"
)

const (
	caFile   = "ca.crt"
	caKey    = "ca.key"
	certFile = "server.crt"
	keyFile  = "server.key"
	// iOS and macOS refuse server certificates valid for more than 825
	// days, even from a user-installed CA.
	serverValidity = 800 * 24 * time.Hour
	caValidity     = 10 * 365 * 24 * time.Hour
	renewBefore    = 30 * 24 * time.Hour
)

// Bundle is what the HTTPS server needs.
type Bundle struct {
	Cert  tls.Certificate
	CAPEM []byte // the CA certificate, for /ca.crt
	Hosts []string
}

// Ensure loads or creates the CA and a server certificate in dir. The
// server certificate is re-issued when it nears expiry or when hosts
// (IP addresses and DNS names) change; the CA is kept, so phones that
// trust it keep working.
func Ensure(dir string, hosts []string, now time.Time) (*Bundle, error) {
	if len(hosts) == 0 {
		return nil, errors.New("tls: no hosts")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	caCert, caPriv, caPEM, err := loadOrCreateCA(dir, now)
	if err != nil {
		return nil, err
	}
	want := normalize(hosts)
	if c, err := tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile)); err == nil {
		leaf, perr := x509.ParseCertificate(c.Certificate[0])
		if perr == nil && leaf.CheckSignatureFrom(caCert) == nil && now.Add(renewBefore).Before(leaf.NotAfter) &&
			slices.Equal(normalize(certHosts(leaf)), want) {
			return &Bundle{Cert: c, CAPEM: caPEM, Hosts: want}, nil
		}
	}
	c, err := issue(dir, caCert, caPriv, want, now)
	if err != nil {
		return nil, err
	}
	return &Bundle{Cert: c, CAPEM: caPEM, Hosts: want}, nil
}

func normalize(hosts []string) []string {
	out := []string{}
	for _, h := range hosts {
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

func certHosts(c *x509.Certificate) []string {
	out := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
}

// writePEM writes atomically: a power loss mid-write must not leave a
// torn CA file, which would make the controller mint a new CA that no
// phone trusts.
func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return fsutil.WriteFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}

func loadOrCreateCA(dir string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certPEM, err1 := os.ReadFile(filepath.Join(dir, caFile))
	keyPEM, err2 := os.ReadFile(filepath.Join(dir, caKey))
	if err1 == nil && err2 == nil {
		cb, _ := pem.Decode(certPEM)
		kb, _ := pem.Decode(keyPEM)
		if cb != nil && kb != nil {
			cert, err := x509.ParseCertificate(cb.Bytes)
			key, kerr := x509.ParseECPrivateKey(kb.Bytes)
			if err == nil && kerr == nil && now.Before(cert.NotAfter) {
				return cert, key, certPEM, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, nil, err
	}
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: "Sprinkler local CA (" + host + ")", Organization: []string{"Sprinkler controller"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := writePEM(filepath.Join(dir, caKey), "EC PRIVATE KEY", kder, 0o600); err != nil {
		return nil, nil, nil, err
	}
	if err := writePEM(filepath.Join(dir, caFile), "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func issue(dir string, ca *x509.Certificate, caPriv *ecdsa.PrivateKey, hosts []string, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	sn, err := serial()
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caPriv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: %w", err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writePEM(filepath.Join(dir, keyFile), "EC PRIVATE KEY", kder, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := writePEM(filepath.Join(dir, certFile), "CERTIFICATE", der, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile))
}
