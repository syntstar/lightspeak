package server

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Landing pages for invite links and the installer download (DE/EN by
// Accept-Language). Static HTML, no scripts.

const installerName = "Lightspeak-setup.exe"

type landingText struct {
	Lang, Title, Lead, Open, Browser, BrowserHint, Download, Steps1, Steps2, Steps3, Steps3Guest, Invalid, Smart, Server string
}

var landingTexts = map[string]landingText{
	"de": {
		Lang: "de", Title: "Lightspeak", Lead: "Du wurdest eingeladen",
		Open: "In Lightspeak öffnen", Download: "Lightspeak für Windows herunterladen",
		Browser:     "Im Browser öffnen",
		BrowserHint: "Ohne Installation: läuft in Chrome, Edge oder Firefox. Für globales Push-to-Talk und den Ton einzelner Apps beim Streamen gibt es die Windows-App.",
		Steps1:      "Lade Lightspeak herunter und installiere es.",
		Steps2:      "Klicke danach auf „In Lightspeak öffnen“ – oder füge den Einladungslink in der App ein.",
		Steps3:      "Wähle Benutzername und Passwort – fertig.",
		Steps3Guest: "Gib deinen Namen ein – fertig. Ein Konto brauchst du nicht.",
		Invalid:     "Diese Einladung ist ungültig oder abgelaufen. Bitte frag nach einem neuen Link.",
		Smart:       "Windows zeigt eventuell „Der Computer wurde durch Windows geschützt“: auf „Weitere Informationen“ und dann „Trotzdem ausführen“ klicken.",
		Server:      "Server",
	},
	"en": {
		Lang: "en", Title: "Lightspeak", Lead: "You have been invited",
		Open: "Open in Lightspeak", Download: "Download Lightspeak for Windows",
		Browser:     "Open in browser",
		BrowserHint: "No installation: runs in Chrome, Edge or Firefox. Global push-to-talk and the sound of single apps when streaming need the Windows app.",
		Steps1:      "Download and install Lightspeak.",
		Steps2:      "Then click “Open in Lightspeak” – or paste the invite link into the app.",
		Steps3:      "Choose a username and password – done.",
		Steps3Guest: "Enter your name – done. You do not need an account.",
		Invalid:     "This invite is invalid or has expired. Please ask for a new link.",
		Smart:       "Windows may show “Windows protected your PC”: click “More info”, then “Run anyway”.",
		Server:      "Server",
	},
}

func pickLang(r *http.Request) landingText {
	al := strings.ToLower(r.Header.Get("Accept-Language"))
	if strings.HasPrefix(al, "de") {
		return landingTexts["de"]
	}
	return landingTexts["en"]
}

var landingTmpl = template.Must(template.New("l").Parse(`<!doctype html>
<html lang="{{.T.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.Title}}</title><meta name="robots" content="noindex">
<style>
:root{--bg:#f5f5f7;--card:#fff;--fg:#1d1d1f;--muted:#6e6e73;--accent:#0a84ff;--border:rgba(0,0,0,.08)}
@media (prefers-color-scheme:dark){:root{--bg:#1c1c1e;--card:#2c2c2e;--fg:#f5f5f7;--muted:#98989d;--border:rgba(255,255,255,.1)}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);
font:15px/1.5 "Segoe UI Variable","Segoe UI",system-ui,sans-serif;padding:16px}
main{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:32px;max-width:440px;width:100%;box-shadow:0 8px 30px rgba(0,0,0,.08)}
h1{font-size:22px;font-weight:600;margin:0 0 4px}p{margin:0 0 12px;color:var(--muted)}.space{font-size:18px;color:var(--fg);font-weight:600}
a.btn{display:block;text-align:center;text-decoration:none;border-radius:8px;padding:10px 14px;margin:10px 0;font-weight:600}
a.primary{background:var(--accent);color:#fff}a.secondary{border:1px solid var(--border);color:var(--fg)}
.logo{display:block;margin:0 0 14px}ol{padding-left:20px;color:var(--muted);margin:16px 0}small{color:var(--muted);display:block;margin-top:12px}
</style></head><body><main>
<svg class="logo" viewBox="0 0 48 48" width="56" height="56" aria-hidden="true"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#0A84FF"/><stop offset="1" stop-color="#BF5AF2"/></linearGradient></defs><rect width="48" height="48" rx="13" fill="url(#g)"/><path d="M14 26v-4M19 30V18M24 33V15M29 30V18M34 26v-4" stroke="#fff" stroke-width="3" stroke-linecap="round"/></svg>
<h1>{{.T.Title}}</h1>
{{if .Invalid}}<p>{{.T.Invalid}}</p>{{else}}
{{if .Code}}<p>{{.T.Lead}}</p>{{if .Space}}<p class="space">{{.Space}}</p>{{end}}
<a class="btn primary" href="lightspeak://invite/{{.Host}}/{{.Code}}">{{.T.Open}}</a>{{end}}
{{if .Web}}<a class="btn secondary" href="/app/{{if .Code}}#invite={{.Code}}{{end}}">{{.T.Browser}}</a>{{end}}
<a class="btn {{if .Code}}secondary{{else}}primary{{end}}" href="{{.DownloadHref}}">{{.T.Download}}</a>
{{if .Web}}<small>{{.T.BrowserHint}}</small>{{end}}
<ol><li>{{.T.Steps1}}</li><li>{{.T.Steps2}}</li><li>{{if .Guest}}{{.T.Steps3Guest}}{{else}}{{.T.Steps3}}{{end}}</li></ol>
<small>{{.T.Smart}}</small>{{end}}
<small>{{.T.Server}}: {{.ServerName}}</small>
</main></body></html>`))

func (s *Server) renderLanding(w http.ResponseWriter, r *http.Request, data map[string]any) {
	data["T"] = pickLang(r)
	// A locally hosted installer wins (private builds); otherwise link the
	// official releases so a server does not have to host the client.
	data["DownloadHref"] = s.cfg.DownloadURL
	if _, err := os.Stat(filepath.Join(s.cfg.PublicDir(), "downloads", installerName)); err == nil || s.cfg.DownloadURL == "" {
		data["DownloadHref"] = "/downloads/" + installerName
	}
	// The app link carries the configured public host, never the request's
	// Host header, so a spoofed header cannot point clients elsewhere.
	data["Host"] = s.cfg.PublicHost()
	data["ServerName"] = s.cfg.ServerName
	data["Web"] = s.webAvailable()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept-Language")
	_ = landingTmpl.Execute(w, data)
}

func (s *Server) landingInvite(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if !s.lim.invitePreview.Allow("ip" + s.clientIP(r)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	iv, err := s.loadInvite(r.Context(), s.db.R, code)
	if err != nil || s.inviteUsable(r.Context(), iv) != nil {
		w.WriteHeader(http.StatusNotFound)
		s.renderLanding(w, r, map[string]any{"Invalid": true})
		return
	}
	space := ""
	if iv.SpaceID != 0 {
		_ = s.db.R.QueryRowContext(r.Context(), `SELECT name FROM spaces WHERE id=?`, iv.SpaceID).Scan(&space)
	}
	guest, _ := s.inviteAdmitsGuests(r.Context(), s.db.R, iv)
	s.renderLanding(w, r, map[string]any{"Code": iv.Code, "Space": space, "Guest": guest})
}

func (s *Server) landingDownload(w http.ResponseWriter, r *http.Request) {
	s.renderLanding(w, r, map[string]any{})
}

// staticDir serves files below PublicDir/<sub> without directory listings.
func (s *Server) staticDir(sub string) http.Handler {
	root := http.Dir(filepath.Join(s.cfg.PublicDir(), sub))
	fs := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fs.ServeHTTP(w, r)
	})
}
