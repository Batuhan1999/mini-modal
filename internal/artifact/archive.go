package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const MaxUploadBytes = 32 << 20
const maxUncompressedBytes = 256 << 20

var ErrInvalidArchive = errors.New("invalid function archive")

func ValidateArchive(archive []byte) error {
	if len(archive) == 0 || len(archive) > MaxUploadBytes {
		return fmt.Errorf("%w: archive must be between 1 byte and 32 MiB", ErrInvalidArchive)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maxUncompressedBytes + 1}
	reader := tar.NewReader(limited)
	files := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		clean := path.Clean(header.Name)
		if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.HasPrefix(header.Name, "\\") {
			return fmt.Errorf("%w: unsafe path %q", ErrInvalidArchive, header.Name)
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			files++
		case tar.TypeDir:
		default:
			return fmt.Errorf("%w: unsupported entry type for %q", ErrInvalidArchive, header.Name)
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		if limited.N == 0 {
			return fmt.Errorf("%w: uncompressed archive exceeds 256 MiB", ErrInvalidArchive)
		}
	}
	if files == 0 {
		return fmt.Errorf("%w: archive contains no files", ErrInvalidArchive)
	}
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if limited.N == 0 {
		return fmt.Errorf("%w: uncompressed archive exceeds 256 MiB", ErrInvalidArchive)
	}
	return nil
}

func ExtractArchive(archive []byte, destination string) error {
	if err := ValidateArchive(archive); err != nil {
		return err
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open function archive: %w", err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read function archive: %w", err)
		}

		name := path.Clean(header.Name)
		if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) || strings.HasPrefix(header.Name, "\\") {
			return fmt.Errorf("%w: unsafe path %q", ErrInvalidArchive, header.Name)
		}
		if name == "." && header.Typeflag == tar.TypeDir {
			continue
		}
		filePath := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filePath, 0o700); err != nil {
				return fmt.Errorf("create artifact directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if name == "." {
				return fmt.Errorf("%w: invalid file path %q", ErrInvalidArchive, header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
				return fmt.Errorf("create artifact directory for %q: %w", name, err)
			}
			file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("create artifact file %q: %w", name, err)
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return fmt.Errorf("write artifact file %q: %w", name, err)
			}
		default:
			return fmt.Errorf("%w: unsupported entry type for %q", ErrInvalidArchive, name)
		}
	}
}
