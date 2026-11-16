package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
)

// Custom CA is optional for HTTPS clients, mandatory for agent mTLS listeners.
func Config(cert, key, ca string, server bool) (*tls.Config, error) {
	c := &tls.Config{MinVersion: tls.VersionTLS13}
	if cert != "" || key != "" {
		pair, e := tls.LoadX509KeyPair(cert, key)
		if e != nil {
			return nil, e
		}
		c.Certificates = []tls.Certificate{pair}
	}
	if ca != "" {
		b, e := os.ReadFile(ca)
		if e != nil {
			return nil, e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid CA PEM")
		}
		if server {
			c.ClientCAs = pool
		} else {
			c.RootCAs = pool
		}
	}
	if server {
		if len(c.Certificates) == 0 || c.ClientCAs == nil {
			return nil, errors.New("agent TLS requires cert, key and client CA")
		}
		c.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return c, nil
}
