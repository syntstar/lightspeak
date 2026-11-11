// Package server implements the Lightspeak HTTP API, the WebSocket gateway,
// file storage and the LiveKit integration.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/syntstar/lightspeak/server/internal/auth"
	"github.com/syntstar/lightspeak/server/internal/config"
	"github.com/syntstar/lightspeak/server/internal/ratelimit"
	"github.com/syntstar/lightspeak/server/internal/store"
)

const ProtocolVersion = 1

// Version is set by main (ldflags).
var Version = "dev"

type limits struct {
	login, register, upload, messages, ws, invitePreview, general *ratelimit.Limiter
}

type Server struct {
	cfg    *config.Config
	db     *store.DB
	log    *slog.Logger
	argon  auth.Params
	hub    *Hub
	voice  *Voice
	files  *Files
	lim    limits
	typing *ratelimit.Limiter
	slow   slowGate
	// webRoot holds the browser client build.
	webRoot string

	now func() time.Time
}

func New(cfg *config.Config, db *store.DB, log *slog.Logger) *Server {
	s := &Server{
		cfg:   cfg,
		db:    db,
		log:   log,
		argon: auth.Params{MemoryKiB: cfg.ArgonMemoryKiB, Time: cfg.ArgonTime, Threads: 1},
		lim: limits{
			login:         ratelimit.PerMinute(5),
			register:      ratelimit.PerHour(3),
			upload:        ratelimit.PerMinute(30),
			messages:      ratelimit.New(10, 20),
			ws:            ratelimit.PerMinute(10),
			invitePreview: ratelimit.PerMinute(30),
			general:       ratelimit.New(20, 60),
		},
		typing:  ratelimit.New(1.0/3, 1),
		webRoot: findWebRoot(executable(), cfg.PublicDir()),
		now:     time.Now,
	}
	s.hub = newHub(s)
	s.voice = newVoice(s)
	s.files = newFiles(s)
	return s
}

// Close stops background work.
func (s *Server) Close() {
	s.voice.stop()
	s.hub.closeAll()
}

// ---------- IDs, JSON, errors ----------

// ID is a snowflake that travels as a JSON string.
type ID int64

func (i ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

func (i *ID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*i = ID(n)
	return nil
}

func (i ID) String() string { return strconv.FormatInt(int64(i), 10) }

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code }

func errf(status int, code, msg string) *apiError { return &apiError{status, code, msg} }

var (
	errUnauthorized = errf(http.StatusUnauthorized, "unauthorized", "authentication required")
	errForbidden    = errf(http.StatusForbidden, "forbidden", "missing permission")
	errBadRequest   = errf(http.StatusBadRequest, "bad_request", "malformed request")
	errRateLimited  = errf(http.StatusTooManyRequests, "rate_limited", "slow down")
	errNotFound     = errf(http.StatusNotFound, "not_found", "not found")
)

func notFound(code string) *apiError { return errf(http.StatusNotFound, code, "not found") }
func badRequest(code string) *apiError {
	return errf(http.StatusBadRequest, code, "invalid request")
}
func conflict(code string) *apiError { return errf(http.StatusConflict, code, "conflict") }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, map[string]any{"error": map[string]string{"code": ae.Code, "message": ae.Message}})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "not found"}})
		return
	}
	s.log.Error("internal error", "path", r.URL.Path, "err", err)
	writeJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "internal error"}})
}

// handler adapts func(w, r) error.
type handler func(w http.ResponseWriter, r *http.Request) error

func (s *Server) h(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			s.writeErr(w, r, err)
		}
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || n <= 0 {
		return 0, notFound("not_found")
	}
	return n, nil
}

func (s *Server) nowMs() int64 { return s.now().UnixMilli() }

// clientIP returns the caller IP (X-Forwarded-For from Caddy on localhost).
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.cfg.TrustForwardedFor && (host == "127.0.0.1" || host == "::1") {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

// ---------- auth context ----------

type ctxKey int

const sessionKey ctxKey = 1

type Session struct {
	ID       int64
	UserID   int64
	DeviceID int64
	Admin    bool
}

func sess(r *http.Request) *Session {
	v, _ := r.Context().Value(sessionKey).(*Session)
	return v
}

const sessionTTL = 90 * 24 * time.Hour

// authenticate resolves a bearer token to a session (sliding expiry).
func (s *Server) authenticate(ctx context.Context, token string) (*Session, error) {
	if token == "" || len(token) > 128 {
		return nil, errUnauthorized
	}
	hash := auth.HashToken(token)
	var se Session
	var dev sql.NullInt64
	var expires, lastUsed int64
	var admin int
	err := s.db.R.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.device_id, s.expires_at, s.last_used_at, u.is_server_admin
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL AND u.disabled_at IS NULL`, hash).
		Scan(&se.ID, &se.UserID, &dev, &expires, &lastUsed, &admin)
	if err != nil {
		return nil, errUnauthorized
	}
	now := s.nowMs()
	if expires < now {
		return nil, errUnauthorized
	}
	se.DeviceID = dev.Int64
	se.Admin = admin == 1
	// Sliding expiry, written at most once per hour to avoid write churn.
	if now-lastUsed > time.Hour.Milliseconds() {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE sessions SET last_used_at=?, expires_at=? WHERE id=?`,
			now, now+sessionTTL.Milliseconds(), se.ID)
		if se.DeviceID != 0 {
			_, _ = s.db.W.ExecContext(ctx, `UPDATE devices SET last_seen_at=? WHERE id=?`, now, se.DeviceID)
		}
	}
	return &se, nil
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			token = s.webToken(r)
		}
		se, err := s.authenticate(r.Context(), token)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if !s.lim.general.Allow("u" + strconv.FormatInt(se.UserID, 10)) {
			s.writeErr(w, r, errRateLimited)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, se)))
	})
}

// ---------- middleware ----------

func (s *Server) originAllowed(o string) bool {
	for _, a := range s.cfg.AllowedOrigins {
		if strings.EqualFold(a, o) {
			return true
		}
	}
	return false
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && s.originAllowed(o) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Filename")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "path", r.URL.Path, "panic", v)
				writeJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "internal error"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Router builds the public HTTP handler (API, WebSocket, files, landing pages).
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverer, securityHeaders, s.cors)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.R.PingContext(r.Context()); err != nil {
			http.Error(w, "db", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"server": Version, "protocolVersion": ProtocolVersion, "minClientVersion": s.cfg.MinClientVersion})
	})
	r.Get("/ws", s.hub.serveWS)
	r.Get("/files/{id}", s.h(s.files.serveFile))
	r.Get("/files/{id}/thumb", s.h(s.files.serveFile))
	r.Get("/avatars/{key}", s.h(s.files.serveAvatar))
	r.Get("/invite/{code}", s.landingInvite)
	r.Get("/download", s.landingDownload)
	r.Get("/", s.landingDownload)
	r.Get("/app", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/app/", http.StatusFound) })
	r.Get("/app/*", s.serveWebApp)
	r.Handle("/updates/*", http.StripPrefix("/updates", s.staticDir("updates")))
	r.Handle("/downloads/*", http.StripPrefix("/downloads", s.staticDir("downloads")))

	r.Route("/api/v1", func(r chi.Router) {
		// Public server description: clients show it before login and check
		// that the address really is a compatible Lightspeak server.
		r.Get("/info", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"product": "lightspeak", "name": s.cfg.ServerName, "server": Version,
				"protocolVersion": ProtocolVersion, "minClientVersion": s.cfg.MinClientVersion, "signup": s.signupPolicy(),
			})
		})
		r.Get("/invites/{code}", s.h(s.getInvite))
		r.Post("/auth/register", s.h(s.register))
		r.Post("/auth/login", s.h(s.login))

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			s.routesAccount(r)
			s.routesSpaces(r)
			s.routesMessages(r)
			s.routesFiles(r)
			s.routesVoice(r)
			s.routesE2EE(r)
		})
	})
	return r
}

// WebhookRouter is served on localhost only (LiveKit → server).
func (s *Server) WebhookRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverer)
	r.Post("/livekit/webhook", s.voice.webhook)
	return r
}

// Start runs background maintenance (orphan uploads, retention, webhook dedupe).
func (s *Server) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		s.maintenance(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.maintenance(ctx)
			}
		}
	}()
}

func (s *Server) maintenance(ctx context.Context) {
	if n, err := s.files.purgeOrphans(ctx, time.Hour); err != nil {
		s.log.Warn("orphan purge failed", "err", err)
	} else if n > 0 {
		s.log.Info("purged orphan uploads", "count", n)
	}
	if s.cfg.RetentionDays > 0 {
		if n, err := s.files.purgeOlderThan(ctx, time.Duration(s.cfg.RetentionDays)*24*time.Hour); err == nil && n > 0 {
			s.log.Info("retention purge", "count", n)
		}
	}
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM webhook_events WHERE received_at < ?`, s.nowMs()-24*time.Hour.Milliseconds())
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ? OR revoked_at < ?`, s.nowMs(), s.nowMs()-30*24*time.Hour.Milliseconds())
}

func (s *Server) audit(ctx context.Context, actor int64, space int64, action string, target int64, meta map[string]any) {
	b, _ := json.Marshal(meta)
	if meta == nil {
		b = []byte("{}")
	}
	var sp, tg any
	if space != 0 {
		sp = space
	}
	if target != 0 {
		tg = target
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO audit_log(id, actor_id, space_id, action, target_id, meta_json, created_at) VALUES (?,?,?,?,?,?,?)`,
		store.NewID(), actor, sp, action, tg, string(b), s.nowMs())
	if err != nil {
		s.log.Warn("audit failed", "err", err)
	}
}

// signupPolicy is the effective LS_SIGNUP value (empty config = admin).
func (s *Server) signupPolicy() string {
	if s.cfg.Signup == "" {
		return config.SignupAdmin
	}
	return s.cfg.Signup
}
