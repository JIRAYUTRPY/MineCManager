package fleet

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"

	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed remote.py
var remoteScript string

type Runner interface {
	Run(context.Context, Host, map[string]any) (json.RawMessage, error)
}
type SSHRunner struct{}

var (
	errSSHNetwork      = errors.New("SSH network connection failed; check the selected IP and port")
	errPrivateKey      = errors.New("invalid SSH private key; an unencrypted key is required")
	errHostKeyMismatch = errors.New("SSH host fingerprint mismatch; verify the host key in the VM console")
	errSSHHandshake    = errors.New("SSH authentication or negotiation failed; check the user, credentials and SSH settings")
)

type boundedBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("remote output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func dial(ctx context.Context, h Host, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	c, e := d.DialContext(ctx, "tcp", net.JoinHostPort(h.Address, strconv.Itoa(h.Port)))
	if e != nil {
		return nil, errSSHNetwork
	}
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	conn, ch, req, e := ssh.NewClientConn(c, net.JoinHostPort(h.Address, strconv.Itoa(h.Port)), cfg)
	if e != nil {
		c.Close()
		return nil, e
	}
	_ = c.SetDeadline(time.Time{})
	return ssh.NewClient(conn, ch, req), nil
}

// Discover obtains a fingerprint without sending user credentials. It does NOT trust it.
func Discover(ctx context.Context, h Host) (string, error) {
	var fingerprint string
	c, e := dial(ctx, h, &ssh.ClientConfig{User: "mcm-host-key-discovery", HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(k)
		return errors.New("discovery complete")
	}})
	if c != nil {
		c.Close()
	}
	if fingerprint != "" {
		return fingerprint, nil
	}
	return "", e
}
func (SSHRunner) Run(ctx context.Context, h Host, input map[string]any) (json.RawMessage, error) {
	auth := []ssh.AuthMethod{}
	if h.PrivateKey != "" {
		k, e := ssh.ParsePrivateKey([]byte(h.PrivateKey))
		if e != nil {
			return nil, errPrivateKey
		}
		auth = append(auth, ssh.PublicKeys(k))
	}
	if h.Password != "" {
		auth = append(auth, ssh.Password(h.Password))
	}
	c, e := dial(ctx, h, &ssh.ClientConfig{User: h.User, Auth: auth, HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
		if ssh.FingerprintSHA256(k) != h.Fingerprint {
			return errHostKeyMismatch
		}
		return nil
	}})
	if e != nil {
		if errors.Is(e, errHostKeyMismatch) || errors.Is(e, errSSHNetwork) {
			return nil, e
		}
		return nil, errSSHHandshake
	}
	defer c.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	session, e := c.NewSession()
	if e != nil {
		return nil, e
	}
	defer session.Close()
	b, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	b = append([]byte("MCM_INPUT_V1\n"), b...)
	command := "python3 -c \"import base64;exec(base64.b64decode('" + base64.StdEncoding.EncodeToString([]byte(remoteScript)) + "'))\""
	if h.User != "root" {
		if h.Password != "" {
			command = "sudo -S -p '' " + command
			b = append([]byte(h.Password+"\n"), b...)
		} else {
			command = "sudo -n " + command
		}
	}
	session.Stdin = bytes.NewReader(b)
	out := &boundedBuffer{max: 4 << 20}
	session.Stdout = out
	// stderr may contain credentials or secrets from files; never persist or expose it.
	session.Stderr = io.Discard
	if e = session.Run(command); e != nil {
		return nil, errors.New("remote operation failed: verify Python 3, sudo rights, OS and connectivity; inspect VM state before retry")
	}
	result := bytes.TrimSpace(out.Bytes())
	if !json.Valid(result) {
		return nil, errors.New("invalid remote response")
	}
	var envelope struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(result, &envelope)
	if envelope.Error != "" {
		return nil, errors.New(strings.TrimSpace(envelope.Error))
	}
	return append(json.RawMessage(nil), result...), nil
}
