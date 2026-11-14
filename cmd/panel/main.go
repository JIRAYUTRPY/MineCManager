package main

import (
 "crypto/sha256"
 "crypto/subtle"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "log"
 "net"
 "net/http"
 "net/url"
 "os"
 "strings"
 "sync"
 "time"
)

type keyConfig struct { ID string `json:"id"`; SHA256 string `json:"sha256"`; Scopes []string `json:"scopes"` }
type nodeConfig struct { ID string `json:"id"`; URL string `json:"url"`; TokenEnv string `json:"token_env"` }
type config struct { Keys []keyConfig `json:"keys"`; Nodes []nodeConfig `json:"nodes"` }
type server struct { cfg config; client *http.Client; audit *os.File; mu sync.Mutex }
type auditEvent struct { At time.Time `json:"at"`; KeyID string `json:"key_id"`; NodeID string `json:"node_id"`; Action string `json:"action"`; Status int `json:"status"` }

func validate(c config) error {
 if len(c.Keys)==0||len(c.Nodes)==0{return errors.New("at least one key and node required")}
 keys:=map[string]bool{};nodes:=map[string]bool{}
 for _,k:=range c.Keys {
  if k.ID==""||keys[k.ID] {return errors.New("duplicate or empty key ID")};keys[k.ID]=true
  b,e:=hex.DecodeString(k.SHA256);if e!=nil||len(b)!=32{return fmt.Errorf("invalid SHA256 for key %s",k.ID)}
  if len(k.Scopes)==0{return fmt.Errorf("missing scopes for key %s",k.ID)}
  for _,scope:=range k.Scopes {if scope!="nodes:read"&&scope!="nodes:control" {return fmt.Errorf("unsupported scope %s",scope)}}
 }
 for _,n:=range c.Nodes {
  if n.ID==""||nodes[n.ID]||n.TokenEnv=="" {return errors.New("duplicate or invalid node")};nodes[n.ID]=true
  u,e:=url.Parse(n.URL);if e!=nil||u.Scheme!="http"||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||u.Path!=""||u.Hostname()=="" {return fmt.Errorf("invalid URL for node %s",n.ID)}
  ip:=net.ParseIP(u.Hostname());if ip==nil||!ip.IsLoopback(){return fmt.Errorf("node %s must use loopback IP",n.ID)}
  if os.Getenv(n.TokenEnv)=="" {return fmt.Errorf("missing token env for node %s",n.ID)}
 }
 return nil
}
func (s *server) authorize(r *http.Request,scope string)(string,bool) {
 header:=r.Header.Get("Authorization")
 if !strings.HasPrefix(header,"Bearer ") {return "",false}
 token:=strings.TrimPrefix(header,"Bearer ")
 if len(token)<32{return "",false}
 hash:=sha256.Sum256([]byte(token))
 for _,k:=range s.cfg.Keys {
  expected,_:=hex.DecodeString(k.SHA256)
  if subtle.ConstantTimeCompare(hash[:],expected)==1 {
   for _,sc:=range k.Scopes {if sc==scope{return k.ID,true}}
   return k.ID,false
  }
 }
 return "",false
}
func (s *server) record(ev auditEvent) {
 s.mu.Lock();defer s.mu.Unlock()
 ev.At=time.Now().UTC()
 data,_:=json.Marshal(ev)
 if _,err:=s.audit.Write(append(data,'\n'));err!=nil {log.Printf("audit write failed: %v",err)}
}
func (s *server) handle(w http.ResponseWriter,r *http.Request){
 // Explicit routes: GET /v1/nodes, GET /v1/nodes/{id}/status, POST /v1/nodes/{id}/actions
 path:=strings.Trim(r.URL.Path,"/")
 parts:=strings.Split(path,"/")
 scope:="nodes:read"
 action:="list"
 if r.Method==http.MethodGet&&path=="v1/nodes" {
  id,ok:=s.authorize(r,scope);if !ok {http.Error(w,"forbidden",403);return}
  out:=make([]map[string]string,0,len(s.cfg.Nodes));for _,n:=range s.cfg.Nodes {out=append(out,map[string]string{"id":n.ID})}
  w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(out)
  s.record(auditEvent{KeyID:id,Action:action,Status:200});return
 }
 if len(parts)!=4||parts[0]!="v1"||parts[1]!="nodes" {http.NotFound(w,r);return}
 nodeID:=parts[2];endpoint:=parts[3]
 if endpoint=="status"&&r.Method==http.MethodGet {action="status"} else if endpoint=="actions"&&r.Method==http.MethodPost {scope="nodes:control";action="control"} else {http.NotFound(w,r);return}
 id,ok:=s.authorize(r,scope);if !ok {http.Error(w,"forbidden",403);s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:403});return}
 var node *nodeConfig
 for i:=range s.cfg.Nodes {if s.cfg.Nodes[i].ID==nodeID {node=&s.cfg.Nodes[i];break}}
 if node==nil {http.NotFound(w,r);s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:404});return}
 target:="/v1/status"
 var body io.Reader
 if action=="control" {
  var payload struct { Action string `json:"action"` }
  if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,4096)).Decode(&payload);err!=nil {http.Error(w,"invalid JSON",400);s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:400});return}
  if payload.Action!="start"&&payload.Action!="stop"&&payload.Action!="restart" {http.Error(w,"invalid action",400);s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:400});return}
  action=payload.Action
  b,_:=json.Marshal(payload);body=strings.NewReader(string(b));target="/v1/actions"
 }
 req,err:=http.NewRequestWithContext(r.Context(),r.Method,node.URL+target,body)
 if err!=nil {http.Error(w,"request failed",500);return}
 req.Header.Set("Authorization","Bearer "+os.Getenv(node.TokenEnv))
 if body!=nil {req.Header.Set("Content-Type","application/json")}
 resp,err:=s.client.Do(req)
 if err!=nil {http.Error(w,"agent unavailable",502);s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:502});return}
 defer resp.Body.Close()
 w.Header().Set("Content-Type","application/json")
 w.WriteHeader(resp.StatusCode)
 _,_=io.Copy(w,io.LimitReader(resp.Body,65536))
 s.record(auditEvent{KeyID:id,NodeID:nodeID,Action:action,Status:resp.StatusCode})
}
func main(){
 path:=os.Getenv("MCM_PANEL_CONFIG");if path=="" {log.Fatal("MCM_PANEL_CONFIG required")}
 data,err:=os.ReadFile(path);if err!=nil {log.Fatal(err)}
 var cfg config
 if err:=json.Unmarshal(data,&cfg);err!=nil {log.Fatal(err)}
 if err:=validate(cfg);err!=nil {log.Fatal(err)}
 auditPath:=os.Getenv("MCM_AUDIT_PATH");if auditPath=="" {log.Fatal("MCM_AUDIT_PATH required")}
 f,err:=os.OpenFile(auditPath,os.O_APPEND|os.O_CREATE|os.O_WRONLY,0600);if err!=nil {log.Fatal(err)};defer f.Close()
 s:=&server{cfg:cfg,audit:f,client:&http.Client{Timeout:8*time.Second,CheckRedirect:func(_ *http.Request,_ []*http.Request)error{return http.ErrUseLastResponse}}}
 mux:=http.NewServeMux();mux.HandleFunc("/v1/",s.handle)
 log.Fatal((&http.Server{Addr:"127.0.0.1:9080",Handler:mux,ReadHeaderTimeout:5*time.Second,MaxHeaderBytes:8192}).ListenAndServe())
}
