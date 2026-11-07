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
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

const cookieName = "sprinkler_session"
const sessionTTL = 180 * 24 * time.Hour

func (s *Server) secret() ([]byte, error) {
	v, err := s.app.Store.RawSetting("session_secret")
	if err != nil {
		return nil, err
	}
	if v == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		v = hex.EncodeToString(b)
		if err := s.app.Store.SetRawSetting("session_secret", v); err != nil {
			return nil, err
		}
	}
	return hex.DecodeString(v)
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
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: payload + "." + sign(sec, payload), Path: "/",
		Expires: time.Unix(exp, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
	return nil
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
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

// isLAN reports whether the request comes from a private or loopback
// address (no proxy headers are trusted).
func isLAN(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

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
