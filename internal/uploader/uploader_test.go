package uploader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeClient struct {
	headErr       error
	headCalls     []string
	listOutputs   []*s3.ListObjectsV2Output
	deleteInputs  []*s3.DeleteObjectsInput
	listCallIndex int
}

func (f *fakeClient) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.headCalls = append(f.headCalls, aws.ToString(params.Key))
	if f.headErr != nil {
		return nil, f.headErr
	}
	return &s3.HeadObjectOutput{}, nil
}

func (f *fakeClient) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listCallIndex >= len(f.listOutputs) {
		return &s3.ListObjectsV2Output{}, nil
	}
	out := f.listOutputs[f.listCallIndex]
	f.listCallIndex++
	return out, nil
}

func (f *fakeClient) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.deleteInputs = append(f.deleteInputs, params)
	return &s3.DeleteObjectsOutput{}, nil
}

type stubUploader struct {
	uploads []*s3.PutObjectInput
	err     error
}

func (s *stubUploader) Upload(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*manager.Uploader)) (*manager.UploadOutput, error) {
	s.uploads = append(s.uploads, input)
	if s.err != nil {
		return nil, s.err
	}
	return &manager.UploadOutput{ETag: aws.String("etag")}, nil
}

func TestBuildPlansIncludesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "nested")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	filePath := filepath.Join(subDir, "data.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	plans, err := BuildPlans([]string{subDir}, "artifact")
	if err != nil {
		t.Fatalf("BuildPlans returned error: %v", err)
	}

	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}

	if plans[0].Key != "artifact/data.txt" {
		t.Errorf("unexpected key %s", plans[0].Key)
	}
}

func TestTransportUploadNoOverwrite(t *testing.T) {
	client := &fakeClient{headErr: nil}
	uploader := &stubUploader{}
	transport := NewTransport(client, uploader, "bucket", false)

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() {
		_ = tmpFile.Close()
	}()

	plans := []FilePlan{{Source: tmpFile.Name(), Key: "existing.txt", Size: 1}}

	_, err = transport.Upload(context.Background(), plans)
	if err == nil {
		t.Fatal("expected error when overwrite disabled and object exists")
	}

	if len(client.headCalls) != 1 {
		t.Fatalf("expected HeadObject to be called once, got %d", len(client.headCalls))
	}
}

func TestTransportUploadAllowsMissingObject(t *testing.T) {
	client := &fakeClient{}
	uploader := &stubUploader{}
	transport := NewTransport(client, uploader, "bucket", false)

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() {
		_ = tmpFile.Close()
	}()

	if _, err := tmpFile.WriteString("hello"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	plans := []FilePlan{{Source: tmpFile.Name(), Key: "new.txt", Size: 5}}

	notFound := &stubAPIError{code: "NotFound"}
	client.headErr = notFound
	res, err := transport.Upload(context.Background(), plans)
	if err != nil {
		t.Fatalf("expected upload to succeed, got error: %v", err)
	}

	if len(res) != 1 {
		t.Fatalf("expected 1 upload result, got %d", len(res))
	}
}

func TestTransportCleanupDeletesObjects(t *testing.T) {
	client := &fakeClient{
		listOutputs: []*s3.ListObjectsV2Output{
			{
				Contents:              []s3types.Object{{Key: aws.String("prefix/file1")}, {Key: aws.String("prefix/file2")}},
				NextContinuationToken: nil,
			},
		},
	}
	transport := NewTransport(client, &stubUploader{}, "bucket", true)

	deleted, err := transport.Cleanup(context.Background(), "prefix")
	if err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted objects, got %d", deleted)
	}
	if len(client.deleteInputs) != 1 {
		t.Fatalf("expected 1 delete request, got %d", len(client.deleteInputs))
	}
}

func TestBuildPlansRejectsDuplicates(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	plans, err := BuildPlans([]string{file, file}, "")
	if err == nil {
		t.Fatal("expected duplicate detection error")
	}
	if plans != nil {
		t.Fatalf("expected nil plans on error")
	}
}

func TestEnsureAbsentIgnoresNotFound(t *testing.T) {
	client := &fakeClient{headErr: errors.New("boom")}
	uploader := &stubUploader{}
	transport := NewTransport(client, uploader, "bucket", false)

	notFoundErr := &stubAPIError{code: "NoSuchKey"}
	client.headErr = notFoundErr
	if err := transport.ensureAbsent(context.Background(), "missing"); err != nil {
		t.Fatalf("expected not found to be ignored, got %v", err)
	}
}

type stubAPIError struct {
	code string
}

func (s *stubAPIError) Error() string {
	return s.code
}

func (s *stubAPIError) ErrorCode() string {
	return s.code
}

func (s *stubAPIError) ErrorMessage() string {
	return s.code
}

func (s *stubAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}
