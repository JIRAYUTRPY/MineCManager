package main

import (
 "crypto/sha256"
 "encoding/hex"
 "net/http"
 "net/http/httptest"
 "os"
 "testing"
)
func TestValidateRejectsRemoteNode(t *testing.T){
 t.Setenv("NODE_TOKEN","secret")
 c:=config{Keys:[]keyConfig{{ID:"admin",SHA256:hex.EncodeToString(make([]byte,32)),Scopes:[]string{"nodes:read"}}},Nodes:[]nodeConfig{{ID:"node1",URL:"http://169.254.169.254",TokenEnv:"NODE_TOKEN"}}}
 if err:=validate(c);err==nil {t.Fatal("accepted remote node")}
}
func TestScopedAuthorization(t *testing.T){
 token:="12345678901234567890123456789012";h:=sha256.Sum256([]byte(token))
 s:=server{cfg:config{Keys:[]keyConfig{{ID:"reader",SHA256:hex.EncodeToString(h[:]),Scopes:[]string{"nodes:read"}}}}}
 r:=httptest.NewRequest(http.MethodGet,"/v1/nodes",nil);r.Header.Set("Authorization","Bearer "+token)
 if _,ok:=s.authorize(r,"nodes:read");!ok {t.Fatal("read denied")}
 if _,ok:=s.authorize(r,"nodes:control");ok {t.Fatal("control allowed")}
}
func TestValidateLoopback(t *testing.T){
 os.Setenv("NODE_TEST_TOKEN","abc");defer os.Unsetenv("NODE_TEST_TOKEN")
 c:=config{Keys:[]keyConfig{{ID:"admin",SHA256:hex.EncodeToString(make([]byte,32)),Scopes:[]string{"nodes:read"}}},Nodes:[]nodeConfig{{ID:"node1",URL:"http://127.0.0.1:9081",TokenEnv:"NODE_TEST_TOKEN"}}}
 if err:=validate(c);err!=nil {t.Fatal(err)}
}
