package wallapop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "__Secure-next-auth.session-token"
	// Re-mint this long before the JWT's exp so a request never carries a token about to expire.
	tokenSlack = 30 * time.Second
)

var errSessionExpired = errors.New("Wallapop session expired: copy a fresh " + sessionCookie + " cookie from es.wallapop.com into WALLAPOP_SESSION")

// session holds the long-lived web cookie and the short-lived API token minted from it.
type session struct {
	mu     sync.Mutex
	seed   string // WALLAPOP_SESSION as configured
	cookie string // Cookie header value; the web rotates it on every mint
	file   string // where the rotated cookie persists across restarts; "" disables
	token  string
	exp    time.Time
	hash   string // hashed_sub claim: my user hash
}

type sessionFile struct {
	Seed   string `json:"seed"`
	Cookie string `json:"cookie"`
}

// newSession prefers the persisted rotated cookie, unless WALLAPOP_SESSION
// changed since it was saved (the user pasted a new one).
func newSession(seed, file string) (*session, error) {
	s := &session{seed: seed, cookie: sessionCookie + "=" + seed, file: file}
	if file == "" {
		return s, nil
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, fmt.Errorf("%s: %w (delete it to start over)", file, err)
	}
	if sf.Seed == seed && sf.Cookie != "" {
		s.cookie = sf.Cookie
	}
	return s, nil
}

func (s *session) persist() error {
	if s.file == "" {
		return nil
	}
	b, err := json.Marshal(sessionFile{Seed: s.seed, Cookie: s.cookie})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

// token returns a valid API token, minting one from the session cookie when
// the cached one is missing, near expiry, or force is set.
func (c *Client) token(ctx context.Context, force bool) (string, error) {
	s := c.sess
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.token != "" && time.Now().Before(s.exp) {
		return s.token, nil
	}
	h := http.Header{"Cookie": {s.cookie}, "Accept": {"application/json"}}
	var res struct {
		Token string `json:"token"`
	}
	rh, err := c.http.Do(ctx, http.MethodGet, c.WebURL+"/api/auth/session", h, nil, &res)
	if err != nil {
		return "", fmt.Errorf("mint Wallapop token: %w", err)
	}
	if res.Token == "" { // an invalid cookie still gets 200, with body {}
		return "", errSessionExpired
	}
	cl, err := parseClaims(res.Token)
	if err != nil {
		return "", fmt.Errorf("mint Wallapop token: %w", err)
	}
	s.token, s.exp, s.hash = res.Token, time.Unix(cl.Exp, 0).Add(-tokenSlack), cl.HashedSub
	if ck := rotatedCookie(rh); ck != "" && ck != s.cookie {
		s.cookie = ck
		if err := s.persist(); err != nil {
			log.Printf("wallapop: saving rotated session cookie: %v", err)
		}
	}
	return s.token, nil
}

// userHash is my user hash, as chat metadata needs it.
func (c *Client) userHash(ctx context.Context) (string, error) {
	if _, err := c.token(ctx, false); err != nil {
		return "", err
	}
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	if c.sess.hash == "" {
		return "", errors.New("Wallapop token has no hashed_sub claim")
	}
	return c.sess.hash, nil
}

type claims struct {
	Exp       int64  `json:"exp"`
	HashedSub string `json:"hashed_sub"`
}

func parseClaims(jwt string) (claims, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return claims{}, errors.New("token is not a JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return claims{}, fmt.Errorf("token payload: %w", err)
	}
	var cl claims
	if err := json.Unmarshal(b, &cl); err != nil {
		return claims{}, fmt.Errorf("token payload: %w", err)
	}
	if cl.Exp == 0 {
		return claims{}, errors.New("token has no exp claim")
	}
	return cl, nil
}

// rotatedCookie rebuilds the Cookie header from the session cookie(s) in a
// response. NextAuth splits large sessions into name.0, name.1, ... chunks.
func rotatedCookie(h http.Header) string {
	var parts []string
	for _, ck := range (&http.Response{Header: h}).Cookies() {
		if ck.Value != "" && (ck.Name == sessionCookie || strings.HasPrefix(ck.Name, sessionCookie+".")) {
			parts = append(parts, ck.Name+"="+ck.Value)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}
