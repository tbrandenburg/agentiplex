// Package security implements the small static-credential deployment boundary:
// operator, viewer and one bearer credential per named agent, resolved once per
// request to an Principal. It is not an identity provider.
package security

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var validID=regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")
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

// Roles of an authenticated request principal.
const (
	RoleOperator="operator"
	RoleViewer="viewer"
	RoleAgent="agent"
)

// Principal is the server-resolved identity of one request. Only Auth.Wrap
// creates it; request bodies never carry identity. A context without a
// principal is trusted local use (library, CLI, loopback demo).
type Principal struct{
	Role string
	AgentID string // set only for RoleAgent
}

type principalKey struct{}

func WithPrincipal(ctx context.Context,p Principal)context.Context{return context.WithValue(ctx,principalKey{},p)}

func PrincipalFrom(ctx context.Context)(Principal,bool){p,ok:=ctx.Value(principalKey{}).(Principal);return p,ok}

// Config names the credential files. AgentFiles maps each logical agent ID to
// its own bearer credential file.
type Config struct{OperatorFile,ViewerFile string;AgentFiles map[string]string}

// Auth resolves a bearer credential to one Principal. Only SHA-256
// digests are kept; raw tokens are never stored or logged.
type Auth struct{
	mu sync.RWMutex
	byHash map[[32]byte]Principal
	hasViewer bool
}

func New(c Config)(*Auth,error){
	if c.OperatorFile==""&&c.ViewerFile==""&&len(c.AgentFiles)==0{return nil,nil}
	if c.OperatorFile==""{return nil,errors.New("operator credential is required when authentication is enabled")}
	a:=&Auth{byHash:map[[32]byte]Principal{}}
	if err:=a.add(Principal{Role:RoleOperator},c.OperatorFile);err!=nil{return nil,err}
	if c.ViewerFile!=""{
		if err:=a.add(Principal{Role:RoleViewer},c.ViewerFile);err!=nil{return nil,err}
		a.hasViewer=true
	}
	ids:=make([]string,0,len(c.AgentFiles))
	for id:=range c.AgentFiles{ids=append(ids,id)}
	sort.Strings(ids)
	for _,id:=range ids{if err:=a.AddAgent(id,c.AgentFiles[id]);err!=nil{return nil,err}}
	return a,nil
}

// AddAgent registers one named agent credential. The ID must be a valid logical
// agent ID (not a reserved role name) and the token unique across all principals.
// Credentials are read at startup; rotate or revoke by editing files and restarting.
func (a *Auth) AddAgent(id,tokenFile string)error{
	if !validID.MatchString(id)||id==RoleOperator||id==RoleViewer{return errors.New("invalid or reserved agent credential ID")}
	return a.add(Principal{Role:RoleAgent,AgentID:id},tokenFile)
}

func (a *Auth) add(p Principal,file string)error{
	token,err:=ReadToken(file);if err!=nil{return err}
	h:=sha256.Sum256([]byte(token))
	a.mu.Lock();defer a.mu.Unlock()
	if _,dup:=a.byHash[h];dup{return errors.New("credentials must be distinct across operator, viewer and agents")}
	a.byHash[h]=p
	return nil
}

func (a *Auth) HasViewer()bool{return a!=nil&&a.hasViewer}

func (a *Auth) resolve(token string)(Principal,bool){
	h:=sha256.Sum256([]byte(token))
	a.mu.RLock();defer a.mu.RUnlock()
	p,ok:=a.byHash[h]
	return p,ok
}

func roleAllowed(role,method,path string)bool{
	if role==RoleOperator{return true}
	read:=method=="GET"||method=="HEAD"
	parts:=strings.Split(strings.TrimPrefix(path,"/"),"/")
	if role==RoleViewer{
		return read&&(strings.HasPrefix(path,"/ui/")||path=="/ui"||path=="/api/v1/dashboard"||path=="/api/v1/usage"||strings.HasPrefix(path,"/api/v1/dashboard/detail/")||path=="/.well-known/ai-catalog.json")
	}
	if role!=RoleAgent{return false}
	if path=="/mcp"||path=="/a2a"{return true}
	if read&&path=="/.well-known/ai-catalog.json"{return true}
	if len(parts)<3||parts[0]!="api"||parts[1]!="v1"{return false}
	switch parts[2]{
	case "agents","groups","pools","usage":
		return read
	case "deliveries":
		return read&&len(parts)==4 // own record by ID; no global listing
	case "messages","broadcasts","dispatches":
		return method=="POST"&&len(parts)==3
	case "state":
		return read||method=="PUT"&&len(parts)==5
	case "state-watch":
		return read
	default:return false // includes the global event feed
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
		w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("Referrer-Policy","no-referrer")
		if r.TLS==nil{http.Error(w,"TLS required",403);return}
		// Browser credentials are exclusively read-only. Block cross-site
		// origins and all browser-origin requests to mutation endpoints.
		if r.Header.Get("Origin")!=""&&(r.Method!="GET"&&r.Method!="HEAD"){http.Error(w,"browser mutation forbidden",403);return}
		var principal Principal
		found:=false
		if scheme,token,ok:=strings.Cut(r.Header.Get("Authorization")," ");ok&&strings.EqualFold(scheme,"Bearer"){
			principal,found=a.resolve(token)
		}else if name,password,ok:=r.BasicAuth();ok&&name=="viewer"{
			// Browsers can only send Basic; accept it for the viewer credential alone.
			if p,ok:=a.resolve(password);ok&&p.Role==RoleViewer{principal,found=p,true}
		}
		if !found{
			if p=="/ui"||strings.HasPrefix(p,"/ui/")||strings.HasPrefix(p,"/api/v1/dashboard"){w.Header().Set("WWW-Authenticate",`Basic realm="Agentiplex dashboard", charset="UTF-8"`)}else{w.Header().Set("WWW-Authenticate",`Bearer realm="Agentiplex"`)}
			http.Error(w,"authentication required",401);return
		}
		if !roleAllowed(principal.Role,r.Method,p){http.Error(w,"forbidden",403);return}
		next.ServeHTTP(w,r.WithContext(WithPrincipal(r.Context(),principal)))
	})
}
