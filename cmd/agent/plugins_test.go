package main

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
)

func TestPluginUploadAndList(t *testing.T){
 root:=t.TempDir()
 if err:=os.Mkdir(filepath.Join(root,"plugins"),0750);err!=nil{t.Fatal(err)}
 a:=agent{root:root}
 req:=httptest.NewRequest(http.MethodPut,"/v1/plugins/Test.jar",bytes.NewReader([]byte("fake jar")))
 req.Header.Set("Content-Type","application/java-archive")
 rec:=httptest.NewRecorder();a.plugin(rec,req)
 if rec.Code!=201 {t.Fatalf("upload %d: %s",rec.Code,rec.Body.String())}
 rec=httptest.NewRecorder();a.plugin(rec,req)
 if rec.Code!=409 {t.Fatalf("overwrite got %d",rec.Code)}
 rec=httptest.NewRecorder();a.plugins(rec,httptest.NewRequest(http.MethodGet,"/v1/plugins",nil))
 var items []pluginInfo
 if err:=json.Unmarshal(rec.Body.Bytes(),&items);err!=nil||len(items)!=1||items[0].Name!="Test.jar"{t.Fatalf("bad list: %s, %v",rec.Body.String(),err)}
}
func TestPluginRejectsTraversalAndSymlinks(t *testing.T){
 root:=t.TempDir();dir:=filepath.Join(root,"plugins")
 if err:=os.Mkdir(dir,0750);err!=nil{t.Fatal(err)}
 a:=agent{root:root}
 for _,name:=range []string{"../evil.jar","bad.txt",".hidden.jar"}{
  rec:=httptest.NewRecorder();a.plugin(rec,httptest.NewRequest(http.MethodGet,"/v1/plugins/"+name,nil))
  if rec.Code!=400 {t.Errorf("%s got %d",name,rec.Code)}
 }
 if err:=os.Symlink(filepath.Join(root,"elsewhere"),filepath.Join(dir,"Evil.jar"));err!=nil{t.Fatal(err)}
 if _,err:=inspectPlugin(dir,"Evil.jar");err==nil {t.Fatal("accepted symlink")}
}
