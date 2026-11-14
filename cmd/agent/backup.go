package main

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "io"
 "net/http"
 "os"
 "path/filepath"
 "strings"
)

type backupEntry struct {
 Path string `json:"path"`
 Size int64 `json:"size"`
 SHA256 string `json:"sha256"`
}
type backupManifest struct {
 Files []backupEntry `json:"files"`
 Skipped []string `json:"skipped"`
}

// Manifest generation does not create an archive or guarantee a consistent snapshot.
// The Minecraft service should be quiesced before a real backup.
func buildBackupManifest(root string)(backupManifest,error){
 base,err:=filepath.EvalSymlinks(root);if err!=nil{return backupManifest{},err}
 result:=backupManifest{Files:[]backupEntry{},Skipped:[]string{}}
 candidates:=[]string{"server.properties","bukkit.yml","spigot.yml","paper-global.yml","paper-world-defaults.yml","ops.json","whitelist.json","banned-players.json","banned-ips.json","plugins","world","world_nether","world_the_end","config"}
 const maxFiles=10000
 const maxFileSize int64=1<<30
 for _,candidate:=range candidates {
  target:=filepath.Join(base,candidate)
  if _,err:=os.Lstat(target);os.IsNotExist(err){continue}else if err!=nil{return result,err}
  err:=filepath.WalkDir(target,func(path string,d os.DirEntry,walkErr error)error{
   if walkErr!=nil{return walkErr}
   rel,err:=filepath.Rel(base,path);if err!=nil{return err}
   if d.Type()&os.ModeSymlink!=0 {result.Skipped=append(result.Skipped,rel+" (symlink)");return nil}
   if d.IsDir(){return nil}
   if !d.Type().IsRegular(){result.Skipped=append(result.Skipped,rel+" (non-regular)");return nil}
   info,err:=d.Info();if err!=nil{return err}
   if info.Size()>maxFileSize {result.Skipped=append(result.Skipped,rel+" (oversize)");return nil}
   if len(result.Files)>=maxFiles {return errTooManyBackupFiles}
   f,err:=os.Open(path);if err!=nil{return err}
   h:=sha256.New();n,copyErr:=io.Copy(h,io.LimitReader(f,maxFileSize+1));closeErr:=f.Close()
   if copyErr!=nil{return copyErr};if closeErr!=nil{return closeErr}
   if n>maxFileSize {result.Skipped=append(result.Skipped,rel+" (changed during scan)");return nil}
   result.Files=append(result.Files,backupEntry{Path:filepath.ToSlash(rel),Size:n,SHA256:hex.EncodeToString(h.Sum(nil))})
   return nil
  })
  if err!=nil{return result,err}
 }
 return result,nil
}
func (a agent) backupManifest(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet {http.Error(w,"method not allowed",405);return}
 manifest,err:=buildBackupManifest(a.root);if err!=nil{
  if strings.Contains(err.Error(),"too many backup files"){http.Error(w,"backup file limit exceeded",413);return}
  http.Error(w,"backup scan failed",500);return
 }
 w.Header().Set("Content-Type","application/json")
 _=json.NewEncoder(w).Encode(manifest)
}
