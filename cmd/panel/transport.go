package main

import (
	"github.com/JIRAYUTRPY/MineCManager/internal/transport"
	"net/http"
	"os"
	"time"
)

func agentClient() (*http.Client, error) {
	tls, e := transport.Config(os.Getenv("MCM_CLIENT_CERT"), os.Getenv("MCM_CLIENT_KEY"), os.Getenv("MCM_CA_CERT"), false)
	if e != nil {
		return nil, e
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = tls
	tr.ResponseHeaderTimeout = 10 * time.Minute
	return &http.Client{Transport: tr, Timeout: 10 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
