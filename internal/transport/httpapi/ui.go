package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

func (s *Server) registerUIRoutes() {
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}

	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", noCache(http.FileServer(http.FS(assets)))))
	s.mux.HandleFunc("GET /{$}", s.dashboard)
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
	page, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}
