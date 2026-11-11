package server

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Browser client: the same Svelte app, served under /app/ from the
// `web` folder next to the server binary (deployed with each release, so the
// app always matches the server and rolls back with it) or, for local
// development, from PublicDir/web. Its session token lives in an HttpOnly
// cookie that scripts never see; the desktop app keeps using bearer tokens.

const (
	// webClientHeader marks requests from the browser client. A custom header
	// cannot be sent cross-site without a CORS preflight (which we never allow
	// for foreign origins), so together with the Origin check and
	// SameSite=Strict it blocks CSRF on cookie-authenticated requests.
	webClientHeader = "X-Lightspeak-Client"
	webClientValue  = "web"
	// Browsers cap cookie lifetimes at 400 days; the server-side session
	// (sliding 90 days, revocable) decides whether the cookie is still valid.
	webCookieMaxAge = 400 * 24 * time.Hour
	webDir          = "web"
)

// webCookieName uses the __Host- prefix on https (Secure, Path=/, no Domain
// enforced by the browser); plain http only exists for local development.
func (s *Server) webCookieName() string {
	if strings.HasPrefix(s.cfg.PublicURL, "https://") {
		return "__Host-ls_session"
	}
	return "ls_session"
}

func isWebClient(r *http.Request) bool {
	return r.Header.Get(webClientHeader) == webClientValue
}

// sameOrigin reports whether the request's Origin is our public origin.
func (s *Server) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o != "" && s.cfg.PublicURL != "" && strings.EqualFold(o, s.cfg.PublicURL)
}

// webToken returns the session cookie of a browser-client request, or "" if
// the request must not be authenticated by cookie: the marker header is
// required, a present Origin must be ours, and state-changing methods must
// carry our Origin (browsers always send it there).
func (s *Server) webToken(r *http.Request) string {
	if !isWebClient(r) {
		return ""
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	if o := r.Header.Get("Origin"); o != "" || !safe {
		if !s.sameOrigin(r) {
			return ""
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return ""
	}
	c, err := r.Cookie(s.webCookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: s.webCookieName(), Value: token, Path: "/",
		MaxAge:   int(webCookieMaxAge / time.Second),
		HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.webCookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
}

// issueToken hands a fresh session token to the caller: browser clients get
// it as cookie only and an empty token in the JSON response.
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request, token string) string {
	if !isWebClient(r) {
		return token
	}
	s.setSessionCookie(w, token)
	return ""
}

// checkWebLogin rejects browser-client logins that do not come from our own
// page (login CSRF) before any work is done.
func (s *Server) checkWebLogin(r *http.Request) error {
	if isWebClient(r) && !s.sameOrigin(r) {
		return errf(http.StatusForbidden, "origin_not_allowed", "origin not allowed")
	}
	return nil
}

// findWebRoot picks the browser client build: next to the binary `exe` if
// its release shipped one, otherwise PublicDir/web.
func findWebRoot(exe, publicDir string) string {
	if exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		d := filepath.Join(filepath.Dir(exe), webDir)
		if st, err := os.Stat(filepath.Join(d, "index.html")); err == nil && st.Mode().IsRegular() {
			return d
		}
	}
	return filepath.Join(publicDir, webDir)
}

func executable() string {
	exe, _ := os.Executable()
	return exe
}

// webAvailable reports whether a browser client build is installed.
func (s *Server) webAvailable() bool {
	st, err := os.Stat(filepath.Join(s.webRoot, "index.html"))
	return err == nil && st.Mode().IsRegular()
}

// webCSP allows exactly what the client needs: own origin (API, gateway,
// files), the LiveKit signalling origin, blob: media, WASM (noise filter).
func (s *Server) webCSP() string {
	connect := "'self'"
	if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Host != "" {
		ws := "wss://"
		if u.Scheme == "http" {
			ws = "ws://"
		}
		connect += " " + ws + u.Host
	}
	if u, err := url.Parse(s.cfg.LiveKitURL); err == nil && u.Host != "" {
		connect += " " + u.Scheme + "://" + u.Host
		// livekit-client also uses the https variant for region/validate calls.
		switch u.Scheme {
		case "wss":
			connect += " https://" + u.Host
		case "ws":
			connect += " http://" + u.Host
		}
	}
	return "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' blob: data:; media-src 'self' blob:; font-src 'self'; worker-src 'self' blob:; " +
		"connect-src " + connect + "; frame-src 'none'; object-src 'none'; base-uri 'none'; " +
		"form-action 'none'; frame-ancestors 'none'"
}

// serveWebApp serves the browser client below /app/. Hashed assets are
// cached forever; every other path gets index.html (no-store) so reloads
// always pick up a new release.
func (s *Server) serveWebApp(w http.ResponseWriter, r *http.Request) {
	root := s.webRoot
	if !s.webAvailable() {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, "/app")), "/")
	h := w.Header()
	h.Set("Content-Security-Policy", s.webCSP())
	h.Set("Permissions-Policy", "microphone=(self), display-capture=(self), camera=(), geolocation=(), usb=(), payment=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	if rel != "" && rel != "index.html" {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !st.IsDir() {
			if strings.HasPrefix(rel, "assets/") {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				h.Set("Cache-Control", "no-cache")
			}
			serveFileAt(w, r, filepath.Join(root, filepath.FromSlash(rel)))
			return
		}
		if strings.HasPrefix(rel, "assets/") || path.Ext(rel) != "" {
			http.NotFound(w, r)
			return
		}
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	serveFileAt(w, r, filepath.Join(root, "index.html"))
}

// serveFileAt serves one file without http.ServeFile's index.html redirects
// and directory handling.
func serveFileAt(w http.ResponseWriter, r *http.Request, p string) {
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
