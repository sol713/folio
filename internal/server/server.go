package server

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"folio/internal/core"
	"folio/internal/i18n"
)

type Server struct {
	Store           *core.Store
	Assets          fs.FS
	Token           string
	DraftToken      string
	ReadToken       string
	ProposalToken   string
	SchedulerPaused bool
}
type envelope struct {
	OK    bool        `json:"ok"`
	Data  any         `json:"data,omitempty"`
	Error *core.Error `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, e error) {
	locale := i18n.Chinese
	if localized, ok := w.(interface{ Locale() string }); ok {
		locale = localized.Locale()
	}
	ce, ok := e.(*core.Error)
	if !ok {
		ce = &core.Error{Code: "internal", Message: "Internal operation failed; inspect local server diagnostics"}
	}
	status := 400
	switch ce.Code {
	case "not_found":
		status = 404
	case "conflict":
		status = 409
	case "unauthorized":
		status = 401
	case "forbidden":
		status = 403
	case "internal":
		status = 500
	}
	localized := *ce
	if ce.Template != "" {
		localized.Message = i18n.Format(locale, ce.Template, ce.Arguments...)
	} else {
		localized.Message = i18n.Message(locale, ce.Message)
	}
	writeJSON(w, status, envelope{Error: &localized})
}
func same(a, b string) bool {
	return b != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Server) role(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if !strings.HasPrefix(a, "Bearer ") {
		return ""
	}
	t := strings.TrimPrefix(a, "Bearer ")
	if same(t, s.Token) {
		return "admin"
	}
	if same(t, s.DraftToken) {
		return "draft"
	}
	if same(t, s.ReadToken) {
		return "read"
	}
	if same(t, s.ProposalToken) {
		return "proposal"
	}
	return ""
}

type localizedWriter struct {
	http.ResponseWriter
	locale string
}

func (w localizedWriter) Locale() string { return w.locale }

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.RequestLocale(r)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			locale = i18n.CookieLocale(r)
		}
		w = localizedWriter{w, locale}
		w.Header().Set("Content-Language", locale)
		w.Header().Set("Vary", "Cookie, Accept-Language, X-Folio-Lang")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		switch {
		case r.URL.Path == "/index.html":
			http.Redirect(w, r, "/", http.StatusMovedPermanently)
		case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
			writeJSON(w, 200, map[string]any{"status": "ok", "version": core.Version})
		case strings.HasPrefix(r.URL.Path, "/api/op/"):
			s.operation(w, r)
		case r.URL.Path == "/api/public/site":
			settings, posts := s.Store.Public()
			writeJSON(w, 200, map[string]any{"settings": settings, "posts": posts})
		case strings.HasPrefix(r.URL.Path, "/api/public/posts/"):
			_, posts := s.Store.Public()
			slug := strings.TrimPrefix(r.URL.Path, "/api/public/posts/")
			for _, p := range posts {
				if p.Slug == slug {
					backlinks := []core.Post{}
					for _, other := range posts {
						if other.ID != p.ID && strings.Contains(other.Markdown, "/posts/"+p.Slug) {
							backlinks = append(backlinks, other)
						}
					}
					writeJSON(w, 200, map[string]any{"post": p, "backlinks": backlinks})
					return
				}
			}
			if target := s.Store.Redirect(slug); target != "" {
				http.Redirect(w, r, "/api/public/posts/"+target, http.StatusMovedPermanently)
				return
			}
			failure(w, core.Err("not_found", "Published post not found"))
		case r.URL.Path == "/api/public/search":
			_, posts := s.Store.Public()
			q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
			if len(q) > 200 {
				failure(w, core.Err("validation", "Query too long"))
				return
			}
			results := []core.Post{}
			for _, p := range posts {
				if q == "" || strings.Contains(strings.ToLower(p.Title+" "+p.Markdown+" "+strings.Join(p.Tags, " ")), q) {
					results = append(results, p)
				}
			}
			writeJSON(w, 200, map[string]any{"posts": results, "query": q})
		case strings.HasPrefix(r.URL.Path, "/media/"):
			s.media(w, r)
		case r.URL.Path == "/rss.xml" || r.URL.Path == "/feed.xml":
			s.feed(w, r)
		case r.URL.Path == "/sitemap.xml":
			s.sitemap(w, r)
		case r.URL.Path == "/robots.txt":
			settings, _ := s.Store.Public()
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /studio\nDisallow: /api/op/\nSitemap: %s/sitemap.xml\n", settings.BaseURL)
		default:
			s.page(w, r)
		}
	})
}
func (s *Server) operation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		failure(w, core.Err("validation", "Use POST with JSON arguments"))
		return
	}
	op := strings.TrimPrefix(r.URL.Path, "/api/op/")
	spec, exists := core.Specs[op]
	if !exists {
		failure(w, core.Err("not_found", "Unknown operation"))
		return
	}
	role := s.role(r)
	if op != "system.capabilities" && role == "" {
		failure(w, core.Err("unauthorized", "A valid Bearer token is required"))
		return
	}
	if op != "system.capabilities" && !allowedOperation(role, spec) {
		failure(w, core.Err("forbidden", "This token cannot perform %s; a publisher/admin token is required", op))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		expectedScheme := "http"
		if base, err := url.Parse(s.Store.Origin()); err == nil && base.Host == r.Host && base.Scheme == "https" {
			expectedScheme = "https"
		}
		if r.TLS != nil {
			expectedScheme = "https"
		}
		if e != nil || u.Host != r.Host || u.Scheme != expectedScheme || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			failure(w, core.Err("forbidden", "Cross-origin administrative request rejected"))
			return
		}
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		typ, _, e := mime.ParseMediaType(ct)
		if e != nil || typ != "application/json" {
			failure(w, core.Err("validation", "Use application/json"))
			return
		}
	}
	limit := int64(8 * 1024 * 1024)
	if op == "migration.plan" || op == "migration.apply" {
		limit = 32 * 1024 * 1024
	}
	if op == "backup.restore" {
		limit = 70 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		failure(w, core.Err("validation", "Request exceeds size limit"))
		return
	}
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if !json.Valid(raw) || strings.TrimSpace(string(raw))[0] != '{' {
		failure(w, core.Err("validation", "Expected one JSON object"))
		return
	}
	data, e := s.Store.Call(r.Context(), op, raw, role)
	if e != nil {
		failure(w, e)
		return
	}
	if op == "system.capabilities" {
		w.Header().Set("Content-Language", "en")
	}
	if op == "system.info" {
		if result, ok := data.(map[string]any); ok {
			result["scheduler"] = map[string]any{"paused": s.SchedulerPaused, "poll_interval_seconds": 1}
			result["actor"] = role
			permitted := []string{}
			for name, entry := range core.Specs {
				if allowedOperation(role, entry) {
					permitted = append(permitted, name)
				}
			}
			sort.Strings(permitted)
			result["permissions"] = permitted
			if ai, ok := result["ai"].(map[string]any); ok {
				if reason, ok := ai["reason"].(string); ok {
					ai["reason"] = i18n.Message(i18n.RequestLocale(r), reason)
				}
			}
		}
	}
	writeJSON(w, 200, envelope{OK: true, Data: data})
}
func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/media/")
	if strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
		http.NotFound(w, r)
		return
	}
	m, ok := s.Store.Media(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, e := base64.StdEncoding.DecodeString(m.Data)
	if e != nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", m.MIME)
	w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("ETag", `"`+m.SHA256+`"`)
	if r.Header.Get("If-None-Match") == `"`+m.SHA256+`"` {
		w.WriteHeader(304)
		return
	}
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}
type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	settings, posts := s.Store.Public()
	v := struct {
		XMLName xml.Name   `xml:"rss"`
		Version string     `xml:"version,attr"`
		Channel rssChannel `xml:"channel"`
	}{Version: "2.0", Channel: rssChannel{Title: settings.Title, Link: settings.BaseURL, Description: settings.Description}}
	for _, p := range posts {
		t, _ := time.Parse(time.RFC3339, p.PublishedAt)
		link := settings.BaseURL + "/posts/" + p.Slug
		v.Channel.Items = append(v.Channel.Items, rssItem{p.Title, link, link, p.HTML, t.Format(time.RFC1123Z)})
	}
	b, _ := xml.MarshalIndent(v, "", "  ")
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(b)
}
func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	settings, posts := s.Store.Public()
	type entry struct {
		Loc     string `xml:"loc"`
		Lastmod string `xml:"lastmod,omitempty"`
	}
	v := struct {
		XMLName xml.Name `xml:"urlset"`
		XMLNS   string   `xml:"xmlns,attr"`
		URLs    []entry  `xml:"url"`
	}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: []entry{{Loc: settings.BaseURL + "/"}}}
	for _, p := range posts {
		v.URLs = append(v.URLs, entry{settings.BaseURL + "/posts/" + p.Slug, p.UpdatedAt})
	}
	b, _ := xml.Marshal(v)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(b)
}
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	asset := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if asset != "." && !strings.Contains(asset, "..") {
		if b, e := fs.ReadFile(s.Assets, asset); e == nil {
			if strings.HasSuffix(asset, ".js") {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			}
			if strings.HasSuffix(asset, ".css") {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			}
			_, _ = w.Write(b)
			return
		}
	}
	b, e := fs.ReadFile(s.Assets, "index.html")
	if e != nil {
		http.Error(w, i18n.Message(i18n.CookieLocale(r), "UI assets unavailable"), 500)
		return
	}
	settings, posts := s.Store.Public()
	title := settings.Title
	description := settings.Description
	canonical := settings.BaseURL + r.URL.Path
	var content strings.Builder
	content.WriteString(`<header><a href="/">` + html.EscapeString(settings.Title) + `</a></header><main id="main">`)
	status := 200
	if strings.HasPrefix(r.URL.Path, "/posts/") {
		slug := strings.TrimPrefix(r.URL.Path, "/posts/")
		found := false
		for _, p := range posts {
			if p.Slug == slug {
				found = true
				title = p.Title + " · " + settings.Title
				description = p.Excerpt
				content.WriteString(`<article><h1>` + html.EscapeString(p.Title) + `</h1><p>` + html.EscapeString(p.Excerpt) + `</p>` + p.HTML + `</article>`)
				break
			}
		}
		if !found {
			if target := s.Store.Redirect(slug); target != "" {
				http.Redirect(w, r, "/posts/"+target, http.StatusMovedPermanently)
				return
			}
			status = 404
			title = i18n.Message(i18n.CookieLocale(r), "Not found") + " · " + settings.Title
			content.WriteString(`<h1>` + i18n.Message(i18n.CookieLocale(r), "That page is not here") + `</h1>`)
		}
	} else {
		content.WriteString(`<h1>` + html.EscapeString(settings.Title) + `</h1><p>` + html.EscapeString(settings.Description) + `</p>`)
		for _, p := range posts {
			content.WriteString(`<article><h2><a href="/posts/` + p.Slug + `">` + html.EscapeString(p.Title) + `</a></h2><p>` + html.EscapeString(p.Excerpt) + `</p></article>`)
		}
	}
	content.WriteString(`</main>`)
	page := string(b)
	for _, lang := range []string{"en", "zh-CN"} {
		page = strings.Replace(page, `<html lang="`+lang+`">`, `<html lang="`+i18n.CookieLocale(r)+`">`, 1)
	}
	start := strings.Index(page, "<title>")
	end := strings.Index(page, "</title>")
	if start >= 0 && end > start {
		page = page[:start] + "<title>" + html.EscapeString(title) + page[end:]
	} else {
		page = strings.Replace(page, "</head>", "<title>"+html.EscapeString(title)+"</title></head>", 1)
	}
	head := `<meta name="description" content="` + html.EscapeString(description) + `"><link rel="canonical" href="` + html.EscapeString(canonical) + `"><meta property="og:title" content="` + html.EscapeString(title) + `"><meta property="og:description" content="` + html.EscapeString(description) + `"><meta property="og:type" content="article"><meta property="og:url" content="` + html.EscapeString(canonical) + `"><link rel="alternate" type="application/rss+xml" title="RSS" href="/rss.xml">`
	if strings.HasPrefix(r.URL.Path, "/studio") {
		head += `<meta name="robots" content="noindex,nofollow">`
		w.Header().Set("Cache-Control", "no-store")
		content.Reset()
	}
	page = strings.Replace(page, "</head>", head+"</head>", 1)
	page = strings.Replace(page, `<div id="app"></div>`, `<div id="app">`+content.String()+`</div>`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = io.WriteString(w, page)
	}
}

func allowedOperation(role string, spec core.Spec) bool {
	if role == "admin" {
		return true
	}
	switch role {
	case "read":
		return spec.Scope == "read" && spec.ReadOnly
	case "draft":
		return spec.Scope == "read" || spec.Scope == "draft" || spec.Scope == "proposal"
	case "proposal":
		return spec.Scope == "read" && spec.ReadOnly || spec.Scope == "proposal"
	}
	return false
}
