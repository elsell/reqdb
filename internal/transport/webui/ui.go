package webui

import (
	"bytes"
	"embed"
	"html"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed assets/*
var files embed.FS

func Handler(version ...string) http.Handler {
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		panic(err)
	}
	value := "dev"
	if len(version) > 0 && version[0] != "" {
		value = version[0]
	}
	index = bytes.ReplaceAll(index, []byte("{{VERSION}}"), []byte(html.EscapeString(value)))
	static := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(index))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=300")
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/assets")
			static.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
