package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAuditRejectsDamagedLog(t *testing.T) {
	for _, data := range []string{`{"status":200}`, "{\"status\":200}\n{broken}\n", "null\n", "[]\n"} {
		p := filepath.Join(t.TempDir(), "audit.jsonl")
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if f, err := openAudit(p); err == nil {
			f.Close()
			t.Fatal("accepted damaged audit log")
		}
		after, _ := os.ReadFile(p)
		if string(after) != data {
			t.Fatal("damaged log modified")
		}
	}
}
func TestOpenAuditPreservesRecords(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	initial := "{\"status\":200}\n"
	os.WriteFile(p, []byte(initial), 0600)
	f, err := openAudit(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := server{audit: f}
	if err = s.record(auditEvent{Status: 204}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b[:len(initial)]) != initial {
		t.Fatal("history overwritten")
	}
}
func TestAuditFailureRemainsClosedUntilRestart(t *testing.T) {
	bad, err := os.CreateTemp(t.TempDir(), "closed-")
	if err != nil {
		t.Fatal(err)
	}
	bad.Close()
	s := server{audit: bad}
	if err = s.record(auditEvent{}); err == nil {
		t.Fatal("closed file accepted")
	}
	good, err := os.CreateTemp(t.TempDir(), "good-")
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	s.audit = good
	if err = s.record(auditEvent{}); err == nil {
		t.Fatal("resumed after uncertain audit write")
	}
	st, _ := good.Stat()
	if st.Size() != 0 {
		t.Fatal("wrote after audit failure")
	}
}
