package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMutualTLS(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	caPath := filepath.Join(dir, "ca.pem")
	os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	issue := func(name string, serial int64, usage x509.ExtKeyUsage) (string, string) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		b, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		cp, kp := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
		os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}), 0600)
		os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600)
		return cp, kp
	}
	sc, sk := issue("server", 2, x509.ExtKeyUsageServerAuth)
	cc, ck := issue("client", 3, x509.ExtKeyUsageClientAuth)
	serverTLS, e := Config(sc, sk, caPath, true)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()
	clientTLS, e := Config(cc, ck, caPath, false)
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: time.Second}
	res, e := client.Get(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	clientTLS.Certificates = nil
	client = &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS.Clone()}, Timeout: time.Second}
	if res, e = client.Get(srv.URL); e == nil {
		res.Body.Close()
		t.Fatal("missing client certificate accepted")
	}
	if _, e = Config(sc, sk, "", true); e == nil {
		t.Fatal("missing CA accepted")
	}
	if serverTLS.MinVersion != tls.VersionTLS13 {
		t.Fatal("TLS policy regressed")
	}
}
