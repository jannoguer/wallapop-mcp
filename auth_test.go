package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenMintCacheAndRotation(t *testing.T) {
	f := newFake(t)
	var cookies []string
	f.wp["GET /api/auth/session"] = func(w http.ResponseWriter, r *http.Request) {
		cookies = append(cookies, r.Header.Get("Cookie"))
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "rotated"})
		w.Write([]byte(`{"token":"` + fakeJWT(time.Now().Add(5*time.Minute), "myhash") + `"}`))
	}
	a := f.authApp(t)
	file := filepath.Join(t.TempDir(), "sub", "session.json")
	a.wp.sess.file = file
	ctx := context.Background()

	tok1, err := a.wp.token(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	tok2, _ := a.wp.token(ctx, false)
	if tok1 != tok2 || len(cookies) != 1 {
		t.Fatalf("cached token not reused: %d mints", len(cookies))
	}
	if cookies[0] != sessionCookie+"=seed" {
		t.Errorf("first mint sent cookie %q", cookies[0])
	}
	a.wp.token(ctx, true)
	if len(cookies) != 2 || cookies[1] != sessionCookie+"=rotated" {
		t.Errorf("forced mint did not use the rotated cookie: %q", cookies)
	}
	if h, _ := a.wp.userHash(ctx); h != "myhash" {
		t.Errorf("user hash %q", h)
	}

	// A restart with the same seed resumes from the persisted rotated cookie;
	// a new seed means the user pasted a fresh cookie.
	s, err := newSession("seed", file)
	if err != nil || s.cookie != sessionCookie+"=rotated" {
		t.Errorf("same seed: cookie %q, err %v", s.cookie, err)
	}
	s, err = newSession("fresh", file)
	if err != nil || s.cookie != sessionCookie+"=fresh" {
		t.Errorf("new seed: cookie %q, err %v", s.cookie, err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Size() == 0 {
		t.Errorf("session file not written: %v", err)
	}
}

func TestTokenNearExpiryRemints(t *testing.T) {
	f := newFake(t)
	f.wp["GET /api/auth/session"] = func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.mints++
		f.mu.Unlock()
		w.Write([]byte(`{"token":"` + fakeJWT(time.Now().Add(10*time.Second), "h") + `"}`))
	}
	a := f.authApp(t)
	a.wp.token(context.Background(), false)
	a.wp.token(context.Background(), false)
	if f.mintCount() != 2 {
		t.Errorf("token inside the expiry slack was reused: %d mints", f.mintCount())
	}
}

func TestSessionExpired(t *testing.T) {
	f := newFake(t)
	f.wp["GET /api/auth/session"] = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }
	cs := connect(t, f.authApp(t))
	res := call(t, cs, "my_items", nil, nil)
	if msg := errorText(res); !strings.Contains(msg, "session expired") {
		t.Errorf("error %q", msg)
	}
}

func TestAuthedRetriesOnceOn401(t *testing.T) {
	f := newFake(t)
	var auths []string
	f.wp["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if len(auths) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[]`))
	}
	a := f.authApp(t)
	if _, err := a.MyItems(context.Background(), MyItemsParams{}); err != nil {
		t.Fatal(err)
	}
	if len(auths) != 2 || f.mintCount() != 2 || !strings.HasPrefix(auths[1], "Bearer ") {
		t.Errorf("requests %q, mints %d", auths, f.mintCount())
	}

	f.wp["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	a = f.authApp(t)
	var he *HTTPError
	if _, err := a.MyItems(context.Background(), MyItemsParams{}); !errors.As(err, &he) || he.Status != 401 {
		t.Errorf("persistent 401: got %v", err)
	}
}

func TestParseClaims(t *testing.T) {
	cl, err := parseClaims(fakeJWT(time.Unix(1700000000, 0), "abc"))
	if err != nil || cl.Exp != 1700000000 || cl.HashedSub != "abc" {
		t.Errorf("got %+v, %v", cl, err)
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c", "a.e30.c"} { // e30 = {} (no exp)
		if _, err := parseClaims(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestRotatedCookieChunks(t *testing.T) {
	h := http.Header{"Set-Cookie": {
		sessionCookie + ".1=b; Path=/",
		"other=x; Path=/",
		sessionCookie + ".0=a; Path=/",
		sessionCookie + "=; Max-Age=0",
	}}
	if got, want := rotatedCookie(h), sessionCookie+".0=a; "+sessionCookie+".1=b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
