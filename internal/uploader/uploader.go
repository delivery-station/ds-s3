package uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// FilePlan represents a local file scheduled for upload.
type FilePlan struct {
	Source string
	Key    string
	Size   int64
}

// UploadResult describes an uploaded object returned to the caller.
type UploadResult struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	ETag   string `json:"etag,omitempty"`
}

// Client captures the subset of S3 methods required by Transport.
type Client interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// Transport coordinates cleanup and upload operations against S3-compatible storage.
type PutUploader interface {
	Upload(ctx context.Context, input *s3.PutObjectInput, opts ...func(*manager.Uploader)) (*manager.UploadOutput, error)
}

type Transport struct {
	client    Client
	uploader  PutUploader
	bucket    string
	overwrite bool
}

// NewTransport builds a Transport.
func NewTransport(client Client, uploader PutUploader, bucket string, overwrite bool) *Transport {
	return &Transport{
		client:    client,
		uploader:  uploader,
		bucket:    bucket,
		overwrite: overwrite,
	}
}

// BuildPlans resolves a set of filesystem paths into upload plans under the desired prefix.
func BuildPlans(paths []string, prefix string) ([]FilePlan, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one source path must be specified")
	}

	plans := make([]FilePlan, 0)
	seen := make(map[string]struct{})
	basePrefix := normalizePrefix(prefix)

	for _, candidate := range paths {
		path := strings.TrimSpace(candidate)
		if path == "" {
			return nil, fmt.Errorf("encountered empty source path entry")
		}

		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to stat %s: %w", path, err)
		}

		if info.IsDir() {
			root := filepath.Clean(path)
			err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return fmt.Errorf("failed to traverse %s: %w", current, walkErr)
				}
				if entry.IsDir() {
					return nil
				}

				fi, err := entry.Info()
				if err != nil {
					return fmt.Errorf("failed to inspect %s: %w", current, err)
				}

				rel, err := filepath.Rel(root, current)
				if err != nil {
					return fmt.Errorf("failed to determine relative path for %s: %w", current, err)
				}

				key := joinKey(basePrefix, filepath.ToSlash(rel))
				if _, dup := seen[key]; dup {
					return fmt.Errorf("duplicate object key detected: %s", key)
				}
				seen[key] = struct{}{}

				plans = append(plans, FilePlan{
					Source: current,
					Key:    key,
					Size:   fi.Size(),
				})
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}

		key := joinKey(basePrefix, filepath.ToSlash(filepath.Base(path)))
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate object key detected: %s", key)
		}
		seen[key] = struct{}{}

		plans = append(plans, FilePlan{
			Source: path,
			Key:    key,
			Size:   info.Size(),
		})
	}

	return plans, nil
}

// Cleanup removes objects under the provided prefix. An empty prefix clears the bucket.
func (t *Transport) Cleanup(ctx context.Context, prefix string) (int, error) {
	total := 0
	var token *string

	resolved := normalizePrefix(prefix)
	if resolved != "" {
		resolved += "/"
	}

	for {
		response, err := t.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(t.bucket),
			Prefix:            stringPointer(resolved),
			ContinuationToken: token,
		})
		if err != nil {
			return total, fmt.Errorf("failed to list objects for cleanup: %w", err)
		}

		if len(response.Contents) == 0 {
			if response.NextContinuationToken == nil {
				return total, nil
			}
			token = response.NextContinuationToken
			continue
		}

		batch := make([]s3types.ObjectIdentifier, 0, len(response.Contents))
		for _, obj := range response.Contents {
			batch = append(batch, s3types.ObjectIdentifier{Key: obj.Key})
		}

		_, err = t.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(t.bucket),
			Delete: &s3types.Delete{Objects: batch, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return total, fmt.Errorf("failed to delete objects: %w", err)
		}

		total += len(batch)

		if response.NextContinuationToken == nil {
			return total, nil
		}
		token = response.NextContinuationToken
	}
}

// Upload executes the planned transfers.
func (t *Transport) Upload(ctx context.Context, plans []FilePlan) ([]UploadResult, error) {
	if len(plans) == 0 {
		return nil, fmt.Errorf("no files provided for upload")
	}

	results := make([]UploadResult, 0, len(plans))

	for _, plan := range plans {
		if !t.overwrite {
			if err := t.ensureAbsent(ctx, plan.Key); err != nil {
				return nil, err
			}
		}

		file, err := os.Open(plan.Source)
		if err != nil {
			return nil, fmt.Errorf("failed to open %s: %w", plan.Source, err)
		}

		contentType := detectContentType(plan.Source, file)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("failed to rewind %s: %w", plan.Source, err)
		}

		output, err := t.uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(t.bucket),
			Key:         aws.String(plan.Key),
			Body:        file,
			ContentType: stringPointer(contentType),
		})

		_ = file.Close()

		if err != nil {
			return nil, fmt.Errorf("failed to upload %s to %s: %w", plan.Source, plan.Key, err)
		}

		results = append(results, UploadResult{
			Source: plan.Source,
			Key:    plan.Key,
			Size:   plan.Size,
			ETag:   aws.ToString(output.ETag),
		})
	}

	return results, nil
}

func (t *Transport) ensureAbsent(ctx context.Context, key string) error {
	_, err := t.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return fmt.Errorf("object %s already exists and overwrite is disabled", key)
	}

	if isNotFound(err) {
		return nil
	}

	return fmt.Errorf("failed to check if %s exists: %w", key, err)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}

	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}

	var ns *s3types.NoSuchKey
	if errors.As(err, &ns) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(apiErr.ErrorCode()) {
		case "notfound", "nosuchkey", "404", "no such key":
			return true
		}
	}

	return false
}

func detectContentType(path string, file *os.File) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		if value := mime.TypeByExtension(ext); value != "" {
			return value
		}
	}

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return http.DetectContentType(buffer[:n])
}

func normalizePrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	return strings.Trim(trimmed, "/")
}

func joinKey(prefix, rel string) string {
	rel = strings.TrimSpace(rel)
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return prefix
	}
	if prefix == "" {
		return rel
	}
	return prefix + "/" + rel
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return aws.String(value)
}
