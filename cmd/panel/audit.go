package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Refuse to append to a damaged log after a crash or partial disk write.
// Operators must preserve the damaged file and reconcile it before restarting.
func openAudit(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("audit log must be a regular file")
	}
	if st.Size() > 0 {
		if _, err = f.Seek(-1, io.SeekEnd); err != nil {
			return nil, err
		}
		var tail [1]byte
		if _, err = io.ReadFull(f, tail[:]); err != nil {
			return nil, err
		}
		if tail[0] != '\n' {
			return nil, errors.New("audit log has an incomplete final record; preserve and reconcile it before restart")
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for scan.Scan() {
			var event map[string]json.RawMessage
			if json.Unmarshal(scan.Bytes(), &event) != nil || event == nil {
				return nil, errors.New("audit log has an invalid record; preserve and reconcile it before restart")
			}
		}
		if err = scan.Err(); err != nil {
			return nil, err
		}
	}
	ok = true
	return f, nil
}
