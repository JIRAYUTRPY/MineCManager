package main

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

type hashWriter struct {
	w io.Writer
	h hash.Hash
}

func newHashWriter(w io.Writer) *hashWriter { return &hashWriter{w: w, h: sha256.New()} }
func (h *hashWriter) Write(b []byte) (int, error) {
	n, e := h.w.Write(b)
	h.h.Write(b[:n])
	return n, e
}
func (h *hashWriter) digest() string { return hex.EncodeToString(h.h.Sum(nil)) }
