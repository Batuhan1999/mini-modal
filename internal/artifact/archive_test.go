package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testArchive(t *testing.T, name string, entryType byte) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: entryType, Mode: 0644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if entryType == tar.TypeReg {
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestValidateArchive(t *testing.T) {
	if err := ValidateArchive(testArchive(t, "src/main.py", tar.TypeReg)); err != nil {
		t.Fatalf("valid archive: %v", err)
	}
	for _, archive := range [][]byte{
		nil,
		[]byte("not a gzip archive"),
		testArchive(t, "../outside.py", tar.TypeReg),
		testArchive(t, "link", tar.TypeSymlink),
	} {
		if err := ValidateArchive(archive); !errors.Is(err, ErrInvalidArchive) {
			t.Fatalf("ValidateArchive returned %v, want ErrInvalidArchive", err)
		}
	}
}

func TestExtractArchive(t *testing.T) {
	destination := t.TempDir()
	if err := ExtractArchive(testArchive(t, "src/main.py", tar.TypeReg), destination); err != nil {
		t.Fatalf("extract valid archive: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "src", "main.py"))
	if err != nil || string(contents) != "x" {
		t.Fatalf("extracted file = %q, error = %v", contents, err)
	}

	if err := ExtractArchive(testArchive(t, "../outside.py", tar.TypeReg), destination); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("extract unsafe archive returned %v, want ErrInvalidArchive", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "..", "outside.py")); !os.IsNotExist(err) {
		t.Fatalf("unsafe file escaped destination: %v", err)
	}
}
