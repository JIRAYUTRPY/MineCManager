package main

import (
 "crypto/subtle"
 "encoding/json"
 "errors"
 "log"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "time"
)

type agent struct { token string; root string; service string }
func (a agent) auth(next http.HandlerFunc) http.HandlerFunc {
 return func(w http.ResponseWriter,r *http.Request) {
  h:=r.Header.Get("Authorization")
  expected:="Bearer "+a.token
  if subtle.ConstantTimeCompare([]byte(h),[]byte(expected))!=1 { http.Error(w,"unauthorized",http.StatusUnauthorized);return }
  next(w,r)
 }
}
func (a agent) execute(w http.ResponseWriter,r *http.Request) {
 if r.Method!=http.MethodPost {http.Error(w,"method not allowed",405);return}
 var body struct { Action string `json:"action"` }
 if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,4096)).Decode(&body);err!=nil {http.Error(w,"invalid json",400);return}
 switch body.Action {case "start","stop","restart": default: http.Error(w,"invalid action",400);return}
 ctx:=r.Context()
 cmd:=exec.CommandContext(ctx,"systemctl",body.Action,a.service)
 if output,err:=cmd.CombinedOutput();err!=nil {log.Printf("systemctl %s failed: %v %s",body.Action,err,output);http.Error(w,"service action failed",502);return}
 w.WriteHeader(204)
}
func (a agent) status(w http.ResponseWriter,r *http.Request) {
 if r.Method!=http.MethodGet {http.Error(w,"method not allowed",405);return}
 output,err:=exec.CommandContext(r.Context(),"systemctl","is-active",a.service).CombinedOutput()
 state:=strings.TrimSpace(string(output))
 if err!=nil && state=="" {state="unknown"}
 w.Header().Set("Content-Type","application/json")
 _=json.NewEncoder(w).Encode(map[string]string{"state":state})
}
func safePath(root,relative string)(string,error){
 if filepath.IsAbs(relative) {return "",errors.New("absolute path denied")}
 clean:=filepath.Clean(relative)
 if clean==".." || strings.HasPrefix(clean,".."+string(os.PathSeparator)){return "",errors.New("path traversal denied")}
 full:=filepath.Join(root,clean)
 resolved,err:=filepath.EvalSymlinks(full);if err!=nil{return "",err}
 base,err:=filepath.EvalSymlinks(root);if err!=nil{return "",err}
 rel,err:=filepath.Rel(base,resolved);if err!=nil||rel==".."||strings.HasPrefix(rel,".."+string(os.PathSeparator)){return "",errors.New("path outside root")}
 return resolved,nil
}
func (a agent) files(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet {http.Error(w,"method not allowed",405);return}
 p,err:=safePath(a.root,r.URL.Query().Get("path"));if err!=nil {http.Error(w,"invalid path",400);return}
 info,err:=os.Stat(p);if err!=nil||!info.IsDir(){http.Error(w,"not a directory",400);return}
 entries,err:=os.ReadDir(p);if err!=nil {http.Error(w,"cannot list directory",500);return}
 type item struct {Name string `json:"name"`; Directory bool `json:"directory"`}
 result:=make([]item,0,len(entries))
 for _,e:=range entries {result=append(result,item{e.Name(),e.IsDir()})}
 w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(result)
}
func main(){
 token:=os.Getenv("MCM_AGENT_TOKEN");root:=os.Getenv("MCM_SERVER_ROOT");service:=os.Getenv("MCM_SYSTEMD_UNIT")
 if len(token)<32||root==""||service=="" {log.Fatal("MCM_AGENT_TOKEN (>=32 chars), MCM_SERVER_ROOT and MCM_SYSTEMD_UNIT required")}
 if !strings.HasSuffix(service,".service")||strings.ContainsAny(service,"/ \\"){log.Fatal("invalid service unit")}
 if info,err:=os.Stat(root);err!=nil||!info.IsDir(){log.Fatal("server root must exist")}
 a:=agent{token,root,service}
 mux:=http.NewServeMux()
 mux.HandleFunc("/v1/status",a.auth(a.status))
 mux.HandleFunc("/v1/actions",a.auth(a.execute))
 mux.HandleFunc("/v1/files",a.auth(a.files))
 mux.HandleFunc("/v1/plugins",a.auth(a.plugins))
 mux.HandleFunc("/v1/plugins/",a.auth(a.plugin))
 s:=&http.Server{Addr:"127.0.0.1:9081",Handler:mux,ReadHeaderTimeout:5*time.Second}
 log.Fatal(s.ListenAndServe())
}
