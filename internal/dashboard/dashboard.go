// Package dashboard embeds the upstream AMIS renderer and trusted schemas.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"
)

//go:generate bash ../../scripts/dashboard-assets.sh
//go:embed static
var assets embed.FS

type Config struct {
	Schema string
	Theme string
	Refresh time.Duration
}

func DefaultConfig() Config { return Config{Theme:"cxd",Refresh:2*time.Second} }

type UI struct {
	root fs.FS
	config []byte
}

func New(cfg Config) (*UI,error) {
	switch cfg.Theme {
	case "cxd","antd","ang","dark":
	default:return nil,fmt.Errorf("unknown dashboard theme %q",cfg.Theme)
	}
	if cfg.Refresh<time.Second||cfg.Refresh>time.Minute{return nil,fmt.Errorf("dashboard refresh must be between 1s and 1m")}
	root,err:=fs.Sub(assets,"static")
	if err!=nil{return nil,err}
	for _,path:=range []string{"vendor/sdk.js","vendor/"+cfg.Theme+".css","vendor/LICENSE"}{
		if _,err=fs.Stat(root,path);err!=nil{return nil,fmt.Errorf("dashboard assets missing: run go generate ./internal/dashboard or disable dashboard: %w",err)}
	}
	raw,err:=fs.ReadFile(root,"dashboard.json")
	if cfg.Schema!=""{
		f,e:=os.Open(cfg.Schema);if e!=nil{return nil,e}
		raw,err=io.ReadAll(io.LimitReader(f,(1<<20)+1));_ = f.Close()
	}
	if err!=nil{return nil,err}
	var schema map[string]any
	if len(raw)>1<<20||json.Unmarshal(raw,&schema)!=nil||schema["type"]!="page"{return nil,fmt.Errorf("dashboard schema must be a JSON page object of at most 1 MiB")}
	if cfg.Schema==""{if body,ok:=schema["body"].([]any);ok{for _,item:=range body{if service,ok:=item.(map[string]any);ok&&service["type"]=="service"{service["interval"]=cfg.Refresh.Milliseconds()}}}}
	data,err:=json.Marshal(map[string]any{"schema":schema,"theme":cfg.Theme,"refreshMs":cfg.Refresh.Milliseconds()})
	if err!=nil{return nil,err}
	return &UI{root:root,config:data},nil
}

func (u *UI) Mount(mux *http.ServeMux){
	files:=http.StripPrefix("/ui/",http.FileServer(http.FS(u.root)))
	mux.HandleFunc("GET /ui/config.json",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Content-Type","application/json");w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("Cache-Control","no-store");_,_=w.Write(u.config)
	})
	mux.Handle("GET /ui/",http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		// AMIS compiles trusted schema expressions and uses inline styles.
		// This is not a sandbox for untrusted schema authors.
		w.Header().Set("Content-Security-Policy","default-src 'none'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data: blob:; font-src 'self' data:; worker-src 'self' blob:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("Referrer-Policy","no-referrer")
		w.Header().Set("Cache-Control","no-cache")
		files.ServeHTTP(w,r)
	}))
}
