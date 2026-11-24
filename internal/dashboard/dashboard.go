// Package dashboard serves a dependency-free, embedded read-only UI.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

func Mount(mux *http.ServeMux){
	root,err:=fs.Sub(assets,"static")
	if err!=nil{panic(err)}
	files:=http.StripPrefix("/ui/",http.FileServer(http.FS(root)))
	mux.Handle("GET /ui/",http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Content-Security-Policy","default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("Referrer-Policy","no-referrer")
		w.Header().Set("Cache-Control","no-cache")
		files.ServeHTTP(w,r)
	}))
}
