package app

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed admin/*
var adminAssets embed.FS

func (s *agentServer) registerAdminRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	assets, err := fs.Sub(adminAssets, "admin")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(assets))
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	})
	mux.Handle("/admin/", http.StripPrefix("/admin/", fileServer))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	})
}
