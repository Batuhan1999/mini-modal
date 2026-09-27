package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

func TestPackagePythonFile(t *testing.T) {
	archive, err := PackagePythonFile("hello_function.py", []byte("print('hello')\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(archive); err != nil {
		t.Fatalf("packaged archive is invalid: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	header, err := tr.Next()
	if err != nil || header.Name != "hello_function.py" {
		t.Fatalf("archive entry = %+v, %v", header, err)
	}
	body, err := io.ReadAll(tr)
	if err != nil || string(body) != "print('hello')\n" {
		t.Fatalf("archive body = %q, %v", body, err)
	}
	for _, name := range []string{"../escape.py", "nested/file.py", "file.txt", "bad\\name.py"} {
		if _, err := PackagePythonFile(name, []byte("x")); !errors.Is(err, ErrInvalidArchive) {
			t.Fatalf("unsafe name %q returned %v", name, err)
		}
	}
}
