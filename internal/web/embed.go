// Package web embeds the built DevPit SPA and serves it as static assets, so
// the single devpit binary hosts both the UI and the REST/SSE API from one
// origin (ADR-0010). The frontend build (frontend/, Vite) writes its output
// into dist/, which is git-ignored; a committed placeholder.html is served
// instead when the SPA has not been built, so `go build` and a fresh run both
// work before any `npm run build`.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// all:dist embeds the Vite build output. dist/ is git-ignored except a
// .gitkeep, so this pattern always matches at least one file on a fresh clone
// (an empty embed pattern is a compile error). The real assets appear here only
// after `npm --prefix frontend run build`.
//
//go:embed all:dist
var dist embed.FS

// placeholder.html is served when dist/index.html is absent (SPA not built).
//
//go:embed placeholder.html
var placeholder []byte

// Handler serves the embedded SPA with history-API fallback: a request for a
// real embedded file (index.html, /assets/*) is served directly, while any
// other path returns index.html so the client-side app boots and a browser
// refresh on any route works (the "correct on refresh" requirement). Missing
// files under /assets/ still 404 rather than masking a broken build with the
// shell. Hashed assets are cached immutably; the HTML shell is never cached.
// When the SPA has not been built, every route serves the placeholder page.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("web: embedded dist unreadable: " + err.Error())
	}
	index, err := fs.ReadFile(sub, "index.html")
	built := err == nil
	fileServer := http.FileServer(http.FS(sub))

	serveShell := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if built {
			_, _ = w.Write(index)
		} else {
			_, _ = w.Write(placeholder)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "index.html" {
			serveShell(w)
			return
		}
		if _, err := fs.Stat(sub, clean); err != nil {
			// Missing /assets/* is a real 404 (broken/absent build); everything
			// else is a client route → serve the SPA shell.
			if strings.HasPrefix(clean, "assets/") {
				http.NotFound(w, r)
				return
			}
			serveShell(w)
			return
		}
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}
