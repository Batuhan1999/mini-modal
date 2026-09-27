package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func TestS3UploadAndVerify(t *testing.T) {
	var uploadedKey string
	var uploadedBytes string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/artifacts/")
		switch r.Method {
		case http.MethodPut:
			uploadedKey = key
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			uploadedBytes = string(body)
		case http.MethodHead:
			if key != uploadedKey {
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	store, err := NewS3Store(context.Background(), Config{
		Endpoint: server.URL, AccessKey: "access", SecretKey: "secret", Bucket: "artifacts",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Upload(context.Background(), "hello", []byte("archive"))
	if err != nil {
		t.Fatal(err)
	}
	if uploadedKey != result.Key || uploadedBytes != "archive" || result.SizeBytes != 7 || result.SHA256 == "" {
		t.Fatalf("unexpected upload: result=%+v key=%q bytes=%q", result, uploadedKey, uploadedBytes)
	}
	if err := store.Verify(context.Background(), "hello", result.Key); err != nil {
		t.Fatalf("verify uploaded artifact: %v", err)
	}
	if err := store.Verify(context.Background(), "other", result.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verify other function returned %v, want ErrNotFound", err)
	}
	if err := store.Verify(context.Background(), "hello", keyFor("hello", strings.Repeat("a", 64))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verify missing artifact returned %v, want ErrNotFound", err)
	}
}

func TestS3StoreMinIO(t *testing.T) {
	if os.Getenv("MINI_MODAL_TEST_S3") != "1" {
		t.Skip("set MINI_MODAL_TEST_S3=1 to test against local MinIO")
	}
	if err := godotenv.Load("../../.env"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := NewS3Store(ctx, Config{
		Endpoint:  os.Getenv("S3_ENDPOINT"),
		AccessKey: os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		Bucket:    os.Getenv("S3_BUCKET"),
	})
	if err != nil {
		t.Fatal(err)
	}
	functionID := "artifact-smoke-" + uuid.NewString()
	archive := testArchive(t, "main.py", tar.TypeReg)
	result, err := store.Upload(ctx, functionID, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: aws.String(store.bucket), Key: aws.String(result.Key),
		}); err != nil {
			t.Errorf("remove smoke-test artifact: %v", err)
		}
	})
	if err := store.Verify(ctx, functionID, result.Key); err != nil {
		t.Fatal(err)
	}
	downloaded, err := store.Download(ctx, result.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, archive) {
		t.Fatal("downloaded artifact differs from uploaded archive")
	}
}
