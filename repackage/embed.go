package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// payload.zip is produced by pack/build.sh. It holds linux/ and pack/
// files of this project only, never Tiiny's app or Electron.
//
//go:embed payload.zip
var payload []byte

func extractPayload(dir string) error {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		target := filepath.Join(dir, file.Name)
		if !inside(dir, target) {
			return errors.New("payload path escapes the unpack directory")
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := file.Mode()
		if mode&0o111 != 0 {
			mode = 0o755
		} else {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return len(rel) < 3 || rel[:3] != "../"
}
