package main
import ("os";"path/filepath";"testing")
func TestSafePath(t *testing.T){
 root:=t.TempDir()
 if err:=os.WriteFile(filepath.Join(root,"ok.txt"),[]byte("ok"),0600);err!=nil {t.Fatal(err)}
 if _,err:=safePath(root,"ok.txt");err!=nil {t.Fatal(err)}
 for _,p:=range []string{"../outside","/etc/passwd"} {if _,err:=safePath(root,p);err==nil {t.Fatalf("accepted %s",p)}}
 outside:=t.TempDir()
 if err:=os.Symlink(outside,filepath.Join(root,"escape"));err!=nil {t.Fatal(err)}
 if _,err:=safePath(root,"escape");err==nil {t.Fatal("accepted escaping symlink")}
}
