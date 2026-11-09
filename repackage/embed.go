package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// extractPayload writes over the previous tree. A file whose content is
// unchanged is left alone, a changed one is replaced by rename (a
// running pcsvr cannot be written in place), and a file the payload no
// longer has is removed.
//
// payload.zip is produced by pack/build.sh. It holds this project's
// linux/ files and the pinned 7-Zip, never Tiiny's app or Electron.
//
//go:embed payload.zip
var payload []byte

func extractPayload(dir string) error {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	want := map[string]bool{filepath.Join(dir, ".payload"): true}
	for _, file := range reader.File {
		target := filepath.Join(dir, file.Name)
		want[target] = true
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
		if unchanged(target, file) {
			continue
		}
		mode := file.Mode()
		if mode&0o111 != 0 {
			mode = 0o755
		} else {
			mode = 0o644
		}
		// Replaced by rename: a running pcsvr cannot be written in place.
		temp := target + ".part"
		out, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
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
		if err := os.Rename(temp, target); err != nil {
			return err
		}
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || want[path] {
			return err
		}
		return os.Remove(path)
	})
}

// unchanged reports whether target already holds file's bytes.
func unchanged(target string, file *zip.File) bool {
	data, err := os.ReadFile(target)
	return err == nil && uint64(len(data)) == file.UncompressedSize64 && crc32.ChecksumIEEE(data) == file.CRC32
}

func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return len(rel) < 3 || rel[:3] != "../"
}
