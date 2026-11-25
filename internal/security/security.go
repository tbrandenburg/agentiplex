// Package security implements the small static-credential deployment boundary.
// It is not an identity provider or a per-agent authorization system.
package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var tokenPattern=regexp.MustCompile("^[A-Za-z0-9._~-]{32,512}$")

// ReadToken reads one bounded, high-entropy operator-provisioned secret.
// Errors never include the secret or path.
func ReadToken(path string)(string,error){
	f,err:=os.Open(path);if err!=nil{return "",errors.New("cannot open credential file")}
	defer f.Close()
	raw,err:=io.ReadAll(io.LimitReader(f,1025));if err!=nil||len(raw)>1024{return "",errors.New("cannot read credential file")}
	token:=strings.TrimSpace(string(raw))
	if !tokenPattern.MatchString(token){return "",errors.New("credential must contain 32–512 URL-safe characters")}
	return token,nil
}

func TLSConfig(caFile string)(*tls.Config,error){
	cfg:=&tls.Config{MinVersion:tls.VersionTLS12}
	if caFile!=""{
		pool,err:=x509.SystemCertPool();if err!=nil{return nil,errors.New("cannot load system roots")}
		raw,err:=os.ReadFile(caFile);if err!=nil||!pool.AppendCertsFromPEM(raw){return nil,errors.New("cannot load CA certificate")}
		cfg.RootCAs=pool
	}
	return cfg,nil
}

type Config struct{OperatorFile,CollaboratorFile,ViewerFile string}

type Auth struct{operator,collaborator,viewer [32]byte;hasViewer bool}

func New(c Config)(*Auth,error){
	if c.OperatorFile==""&&c.CollaboratorFile==""&&c.ViewerFile==""{return nil,nil}
	if c.OperatorFile==""||c.CollaboratorFile==""{return nil,errors.New("operator and collaborator credentials are both required")}
	read:=func(path string)([32]byte,error){s,e:=ReadToken(path);return sha256.Sum256([]byte(s)),e}
	a:=&Auth{}
	var err error
	if a.operator,err=read(c.OperatorFile);err!=nil{return nil,err}
	if a.collaborator,err=read(c.CollaboratorFile);err!=nil{return nil,err}
	if a.operator==a.collaborator{return nil,errors.New("role credentials must be distinct")}
	if c.ViewerFile!=""{
		a.viewer,err=read(c.ViewerFile);if err!=nil{return nil,err}
		if a.viewer==a.operator||a.viewer==a.collaborator{return nil,errors.New("role credentials must be distinct")}
		a.hasViewer=true
	}
	return a,nil
}

func (a *Auth) HasViewer()bool{return a!=nil&&a.hasViewer}
func equal(hash [32]byte,token string)bool{
	actual:=sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(hash[:],actual[:])==1
}

func roleAllowed(role,method,path string)bool{
	if role=="operator"{return true}
	read:=method=="GET"||method=="HEAD"
	parts:=strings.Split(strings.TrimPrefix(path,"/"),"/")
	if role=="viewer"{
		return read&&(strings.HasPrefix(path,"/ui/")||path=="/ui"||path=="/api/v1/dashboard"||strings.HasPrefix(path,"/api/v1/dashboard/detail/")||path=="/.well-known/ai-catalog.json")
	}
	if role!="collaborator"{return false}
	if path=="/mcp"||path=="/a2a"{return true}
	if read&&path=="/.well-known/ai-catalog.json"{return true}
	if len(parts)<3||parts[0]!="api"||parts[1]!="v1"{return false}
	switch parts[2]{
	case "agents","groups","pools","deliveries","events":
		return read
	case "messages","broadcasts","dispatches":
		return method=="POST"&&len(parts)==3
	case "state":
		return read||method=="PUT"&&len(parts)==5
	case "state-watch":
		return read
	default:return false
	}
}

// Wrap authorizes before dispatch to any router. Reject escaped paths and
// noncanonical path segments to avoid discrepancies with ServeMux routing.
func (a *Auth) Wrap(next http.Handler)http.Handler{
	if a==nil{return next}
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		p:=r.URL.Path
		if r.URL.EscapedPath()!=p||strings.Contains(p,"//")||strings.Contains(p,"\\")||strings.Contains(p,"/../")||strings.Contains(p,"/./")||strings.HasSuffix(p,"/..")||strings.HasSuffix(p,"/."){
			http.Error(w,"invalid path",400);return
		}
		if (r.Method=="GET"||r.Method=="HEAD")&&(p=="/healthz"||p=="/readyz"||p=="/.well-known/agent-card.json"){
			next.ServeHTTP(w,r);return
		}
		w.Header().Set("Cache-Control","no-store")
		if r.TLS==nil{http.Error(w,"TLS required",403);return}
		// Browser credentials are exclusively read-only. Block cross-site
		// origins and all browser-origin requests to mutation endpoints.
		if r.Header.Get("Origin")!=""&&(r.Method!="GET"&&r.Method!="HEAD"){http.Error(w,"browser mutation forbidden",403);return}
		role:=""
		if scheme,token,ok:=strings.Cut(r.Header.Get("Authorization")," ");ok&&strings.EqualFold(scheme,"Bearer"){
			if equal(a.operator,token){role="operator"}else if equal(a.collaborator,token){role="collaborator"}else if a.hasViewer&&equal(a.viewer,token){role="viewer"}
		}else if name,password,ok:=r.BasicAuth();ok&&name=="viewer"&&a.hasViewer&&equal(a.viewer,password){role="viewer"}
		if role==""{
			if p=="/ui"||strings.HasPrefix(p,"/ui/")||strings.HasPrefix(p,"/api/v1/dashboard"){w.Header().Set("WWW-Authenticate",`Basic realm="Agentiplex dashboard", charset="UTF-8"`)}else{w.Header().Set("WWW-Authenticate",`Bearer realm="Agentiplex"`)}
			http.Error(w,"authentication required",401);return
		}
		if !roleAllowed(role,r.Method,p){http.Error(w,"forbidden",403);return}
		next.ServeHTTP(w,r)
	})
}
