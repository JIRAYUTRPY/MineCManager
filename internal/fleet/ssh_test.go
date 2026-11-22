package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSSHHostPinningAndInputFraming(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	seen := make(chan string, 1)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				c, chans, reqs, e := ssh.NewServerConn(conn, cfg)
				if e != nil {
					conn.Close()
					return
				}
				defer c.Close()
				go ssh.DiscardRequests(reqs)
				for n := range chans {
					ch, requests, e := n.Accept()
					if e != nil {
						return
					}
					for req := range requests {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						req.Reply(true, nil)
						body, _ := io.ReadAll(ch)
						seen <- string(body)
						_, _ = ch.Write([]byte(`{"cpu_count":4}`))
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						ch.Close()
						break
					}
				}
			}()
		}
	}()
	port, _ := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
	h := Host{Address: "127.0.0.1", Port: port, User: "root", Password: "secret", Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	discovered, e := Discover(ctx, h)
	if e != nil || discovered != h.Fingerprint {
		t.Fatal("discovery", e)
	}
	result, e := (SSHRunner{}).Run(ctx, h, map[string]any{"action": "metrics"})
	if e != nil || !json.Valid(result) {
		t.Fatal(string(result), e)
	}
	if input := <-seen; !strings.HasPrefix(input, "MCM_INPUT_V1\n") || strings.Contains(input, "secret") {
		t.Fatal("bad input frame", input)
	}
	h.Fingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, e = (SSHRunner{}).Run(ctx, h, map[string]any{"action": "metrics"}); !errors.Is(e, errHostKeyMismatch) {
		t.Fatalf("host key mismatch was not identified: %v", e)
	}
}

func TestSSHOutputBudgetAcrossStreamCopies(t *testing.T) {
	out := &boundedBuffer{max: 8}
	for _, part := range []string{"first", "two"} {
		if _, err := io.Copy(out, io.LimitReader(strings.NewReader(part), int64(len(part)))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.Copy(out, io.LimitReader(strings.NewReader("x"), 1)); err == nil {
		t.Fatal("stdout stream exceeded its configured budget")
	}
	if got := out.data.String(); got != "firsttwo" {
		t.Fatalf("buffer changed: %q", got)
	}
}
