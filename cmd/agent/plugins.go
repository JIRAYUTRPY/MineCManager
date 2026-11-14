package main

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "os"
 "path/filepath"
 "regexp"
 "strings"
)

const maxPluginBytes int64 = 16 << 20
var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.jar$`)

type pluginInfo struct {
 Name string `json:"name"`
 Size int64 `json:"size"`
 SHA256 string `json:"sha256"`
}

func pluginDirectory(root string) (string,error) {
 base,err:=filepath.EvalSymlinks(root);if err!=nil{return "",err}
 p:=filepath.Join(base,"plugins")
 info,err:=os.Lstat(p);if err!=nil{return "",err}
 if !info.IsDir()||info.Mode()&os.ModeSymlink!=0{return "",errors.New("invalid plugins directory")}
 return p,nil
}
func pluginFile(dir,name string)(string,error){
 if !pluginNamePattern.MatchString(name)||strings.HasPrefix(name,".") {return "",errors.New("invalid plugin filename")}
 return filepath.Join(dir,name),nil
}
func inspectPlugin(dir,name string)(pluginInfo,error){
 path,err:=pluginFile(dir,name);if err!=nil{return pluginInfo{},err}
 info,err:=os.Lstat(path);if err!=nil{return pluginInfo{},err}
 if !info.Mode().IsRegular()||info.Size()>maxPluginBytes {return pluginInfo{},errors.New("unsupported plugin file")}
 f,err:=os.Open(path);if err!=nil{return pluginInfo{},err};defer f.Close()
 h:=sha256.New();n,err:=io.Copy(h,io.LimitReader(f,maxPluginBytes+1));if err!=nil||n>maxPluginBytes{return pluginInfo{},errors.New("cannot hash plugin")}
 return pluginInfo{Name:name,Size:n,SHA256:hex.EncodeToString(h.Sum(nil))},nil
}
func (a agent) plugins(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet {http.Error(w,"method not allowed",405);return}
 dir,err:=pluginDirectory(a.root);if err!=nil{http.Error(w,"plugins directory unavailable",503);return}
 entries,err:=os.ReadDir(dir);if err!=nil{http.Error(w,"cannot list plugins",500);return}
 result:=[]pluginInfo{}
 for _,entry:=range entries {
  if !pluginNamePattern.MatchString(entry.Name()) {continue}
  item,err:=inspectPlugin(dir,entry.Name());if err==nil{result=append(result,item)}
 }
 w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(result)
}
func (a agent) plugin(w http.ResponseWriter,r *http.Request){
 name:=strings.TrimPrefix(r.URL.Path,"/v1/plugins/")
 dir,err:=pluginDirectory(a.root);if err!=nil{http.Error(w,"plugins directory unavailable",503);return}
 path,err:=pluginFile(dir,name);if err!=nil{http.Error(w,"invalid plugin name",400);return}
 switch r.Method {
 case http.MethodGet:
  item,err:=inspectPlugin(dir,name);if err!=nil{http.NotFound(w,r);return}
  w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(item)
 case http.MethodPut:
  // New plugins only. Replacements need a separate versioned rollback design.
  if r.Header.Get("Content-Type")!="application/java-archive" {http.Error(w,"expected application/java-archive",415);return}
  if _,err:=os.Lstat(path);err==nil{http.Error(w,"plugin already exists",409);return}else if !os.IsNotExist(err){http.Error(w,"cannot inspect plugin",500);return}
  tmp,err:=os.CreateTemp(dir,".mcm-upload-*");if err!=nil{http.Error(w,"cannot stage plugin",500);return}
  defer os.Remove(tmp.Name())
  defer tmp.Close()
  n,err:=io.Copy(tmp,io.LimitReader(r.Body,maxPluginBytes+1))
  if err!=nil||n==0||n>maxPluginBytes {http.Error(w,"invalid plugin size",413);return}
  if err:=tmp.Sync();err!=nil{http.Error(w,"cannot sync plugin",500);return}
  if err:=tmp.Chmod(0640);err!=nil{http.Error(w,"cannot set plugin permissions",500);return}
  // Hard-link is atomic and fails when destination exists, avoiding overwrite races.
  if err:=os.Link(tmp.Name(),path);err!=nil{
   if errors.Is(err,os.ErrExist){http.Error(w,"plugin already exists",409);return}
   http.Error(w,fmt.Sprintf("cannot publish plugin: %v",err),500);return
  }
  item,err:=inspectPlugin(dir,name);if err!=nil{http.Error(w,"cannot verify plugin",500);return}
  w.Header().Set("Content-Type","application/json");w.WriteHeader(201);_=json.NewEncoder(w).Encode(item)
 default: http.Error(w,"method not allowed",405)
 }
}
