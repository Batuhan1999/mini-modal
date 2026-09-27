package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"path"
	"strings"
)

func PackagePythonFile(name string, source []byte) ([]byte, error) {
	if name == "" || path.Base(name) != name || strings.ContainsAny(name, "\\\x00") || !strings.HasSuffix(name, ".py") {
		return nil, fmt.Errorf("%w: use a .py filename without a path", ErrInvalidArchive)
	}
	if len(source) == 0 || len(source) > MaxUploadBytes {
		return nil, fmt.Errorf("%w: Python file must be between 1 byte and 32 MiB", ErrInvalidArchive)
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(source)), Typeflag: tar.TypeReg}); err != nil {
		return nil, fmt.Errorf("package Python file: %w", err)
	}
	if _, err := tw.Write(source); err != nil {
		return nil, fmt.Errorf("package Python file: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("package Python file: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("package Python file: %w", err)
	}
	return buffer.Bytes(), nil
}
