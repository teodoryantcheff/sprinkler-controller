package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: OWASP minimum (19 MiB, t=2) so a login stays quick
// on a Pi 1 with 512 MB.
const (
	argonMem  = 19 * 1024
	argonTime = 2
	argonPar  = 1
	argonLen  = 32
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, argonTime, argonMem, argonPar, argonLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMem, argonTime, argonPar,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}

// CheckPassword verifies pw against an encoded argon2id hash.
func CheckPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	if len(want) == 0 || len(want) > 1024 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) //nolint:gosec // G115: length bounded above
	return subtle.ConstantTimeCompare(got, want) == 1
}

const (
	cookieName = "sprinkler_session"
	sessionTTL = 180 * 24 * time.Hour
)

// secret returns the session signing key, creating it on first use. It is
// read from the store each time (one indexed row) so "sprinkler passwd"
// can log everyone out without a restart.
func (s *Server) secret() ([]byte, error) {
	s.secretMu.Lock()
	defer s.secretMu.Unlock()
	v, err := s.app.Store.RawSetting("session_secret")
	if err != nil {
		return nil, err
	}
	if v == "" {
		return s.newSecretLocked()
	}
	return hex.DecodeString(v)
}

// rotateSecret replaces the session key, which logs out every session.
func (s *Server) rotateSecret() error {
	s.secretMu.Lock()
	defer s.secretMu.Unlock()
	_, err := s.newSecretLocked()
	return err
}

func (s *Server) newSecretLocked() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := s.app.Store.SetRawSetting("session_secret", hex.EncodeToString(b)); err != nil {
		return nil, err
	}
	return b, nil
}

func sign(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, user string) error {
	sec, err := s.secret()
	if err != nil {
		return err
	}
	exp := time.Now().Add(sessionTTL).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + strconv.FormatInt(exp, 10)
	// Secure only over HTTPS: the UI is also served over plain HTTP on the LAN.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see above
		Name: cookieName, Value: payload + "." + sign(sec, payload), Path: "/",
		Expires: time.Unix(exp, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
	return nil
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode}) //nolint:gosec // G124: deletes the cookie
}

// sessionUser returns the logged-in user name, or "".
func (s *Server) sessionUser(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return ""
	}
	sec, err := s.secret()
	if err != nil {
		return ""
	}
	if !hmac.Equal([]byte(sign(sec, parts[0]+"."+parts[1])), []byte(parts[2])) {
		return ""
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return ""
	}
	u, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	return string(u)
}

// clientIP is the TCP peer address (no proxy headers are trusted).
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// isLAN reports whether the request comes from a private or loopback
// address and was sent to a local name, so a public DNS name rebound to
// the controller's address can't use LAN access from a web page.
func isLAN(r *http.Request) bool {
	ip := clientIP(r)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) && localHost(r.Host)
}

// localHost reports whether a Host header names the controller on the
// local network: an IP address, a single-label name, or a name under a
// suffix that public DNS doesn't serve.
func localHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.ToLower(strings.Trim(h, "[]")), ".")
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return true
	}
	for _, suf := range []string{".local", ".lan", ".home.arpa", ".internal", ".localdomain"} {
		if strings.HasSuffix(h, suf) {
			return true
		}
	}
	return false
}

// Login throttling: after maxFailures wrong passwords from one address
// within failWindow, that address is refused until the window passes.
// hashSlots bounds concurrent argon2 runs (19 MiB each on a 512 MB Pi).
const (
	maxFailures = 10
	failWindow  = 15 * time.Minute
	hashSlots   = 2
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func limiterKey(r *http.Request) string {
	if ip := clientIP(r); ip != nil {
		return ip.String()
	}
	return r.RemoteAddr
}

// blocked reports whether the caller has too many recent failures.
func (l *loginLimiter) blocked(r *http.Request, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recentLocked(limiterKey(r), now)) >= maxFailures
}

func (l *loginLimiter) fail(r *http.Request, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails = map[string][]time.Time{}
	}
	if len(l.fails) > 1000 { // bound memory: drop stale entries
		for k := range l.fails {
			l.recentLocked(k, now)
		}
	}
	k := limiterKey(r)
	l.fails[k] = append(l.recentLocked(k, now), now)
}

func (l *loginLimiter) success(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, limiterKey(r))
}

// recentLocked drops failures older than failWindow and returns the rest.
func (l *loginLimiter) recentLocked(k string, now time.Time) []time.Time {
	ts := l.fails[k]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > failWindow {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, k)
	} else {
		l.fails[k] = ts
	}
	return ts
}

// checkPassword verifies a password with at most hashSlots argon2 runs at
// once. An empty hash (unknown user) is checked against a dummy hash so
// the reply takes as long as for a real user.
func (s *Server) checkPassword(hash, pw string) bool {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	if hash == "" {
		CheckPassword(dummyHash(), pw)
		return false
	}
	return CheckPassword(hash, pw)
}

func (s *Server) hashPassword(pw string) (string, error) {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return HashPassword(pw)
}

var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("not a password")
	return h
})

var errUnauthorized = errors.New("login required")

// authorized reports who the caller is, honouring LAN-only access.
func (s *Server) authorized(r *http.Request) (string, bool) {
	if u := s.sessionUser(r); u != "" {
		return u, true
	}
	st, _, _, _ := s.app.Snapshot()
	if st.LANNoLogin && isLAN(r) {
		return "lan", true
	}
	return "", false
}
