package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (s *Service) cloudflare(ctx context.Context, c Cloud, method, path string, body any) (json.RawMessage, error) {
	if c.Token == "" {
		return nil, errors.New("configure Cloudflare DNS token first")
	}
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, &buf)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Cloudflare connection failed")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return nil, e
	}
	var envelope struct {
		Success bool `json:"success"`
	}
	if resp.StatusCode/100 != 2 || json.Unmarshal(b, &envelope) != nil || !envelope.Success {
		return nil, fmt.Errorf("Cloudflare rejected request (HTTP %d); verify token zone permissions and record data", resp.StatusCode)
	}
	return b, nil
}
func (s *Service) dns(ctx context.Context, c Cloud, q map[string]any) (json.RawMessage, error) {
	zone := stringField(q, "zone")
	name := stringField(q, "name")
	kind := stringField(q, "type")
	content := stringField(q, "content")
	record := stringField(q, "record_id")
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(zone) || len(name) > 253 || name == "" {
		return nil, errors.New("zone ID and DNS name required")
	}
	path := "/zones/" + zone + "/dns_records"
	method := "POST"
	if record != "" {
		if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(record) {
			return nil, errors.New("invalid record ID")
		}
		path += "/" + record
		method = "PUT"
	}
	body := map[string]any{"name": name, "type": kind, "ttl": 300, "proxied": false}
	switch kind {
	case "A":
		if ip := net.ParseIP(content); ip == nil || ip.To4() == nil {
			return nil, errors.New("A requires IPv4")
		}
		body["content"] = content
	case "AAAA":
		if ip := net.ParseIP(content); ip == nil || ip.To4() != nil {
			return nil, errors.New("AAAA requires IPv6")
		}
		body["content"] = content
	case "CNAME":
		if content == "" {
			return nil, errors.New("CNAME target required")
		}
		body["content"] = content
	case "SRV":
		data, ok := q["data"].(map[string]any)
		if !ok {
			return nil, errors.New("SRV data required")
		}
		body["data"] = data
	default:
		return nil, errors.New("supported DNS types: A, AAAA, CNAME, SRV")
	}
	// Game records always use DNS only. Explicit web=true enables HTTP proxy for web records.
	if q["web"] == true && kind != "SRV" {
		body["proxied"] = true
	}
	return s.cloudflare(ctx, c, method, path, body)
}
func r2Client(c Cloud) (*s3.Client, error) {
	if c.Account == "" || c.AccessKey == "" || c.SecretKey == "" || c.Bucket == "" {
		return nil, errors.New("configure R2 account, bucket and S3 credentials first")
	}
	return s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String("https://" + c.Account + ".r2.cloudflarestorage.com"), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}), nil
}
func uploadURL(ctx context.Context, c Cloud, key string) (string, error) {
	client, e := r2Client(c)
	if e != nil {
		return "", e
	}
	signed, e := s3.NewPresignClient(client).PresignPutObject(ctx, &s3.PutObjectInput{Bucket: &c.Bucket, Key: &key}, func(o *s3.PresignOptions) { o.Expires = time.Hour })
	if e != nil {
		return "", errors.New("R2 signing failed")
	}
	return signed.URL, nil
}
func (s *Service) r2Read(ctx context.Context, c Cloud, q map[string]any, action string) (json.RawMessage, error) {
	client, e := r2Client(c)
	if e != nil {
		return nil, e
	}
	if action == "r2-list" {
		prefix := "minecmanager/"
		out, e := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &c.Bucket, Prefix: &prefix, MaxKeys: aws.Int32(1000)})
		if e != nil {
			return nil, errors.New("R2 list failed; verify bucket permissions")
		}
		return json.Marshal(out)
	}
	key := stringField(q, "key")
	if !strings.HasPrefix(key, "minecmanager/") || strings.Contains(key, "..") {
		return nil, errors.New("invalid managed backup key")
	}
	signed, e := s3.NewPresignClient(client).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &c.Bucket, Key: &key}, func(o *s3.PresignOptions) { o.Expires = 10 * time.Minute })
	if e != nil {
		return nil, errors.New("R2 signing failed")
	}
	return json.Marshal(map[string]string{"url": signed.URL, "expires": "10 minutes; treat as a secret"})
}
