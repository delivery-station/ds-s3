package config

import (
	"context"
	"testing"

	"github.com/delivery-station/ds/pkg/types"
)

type stubHostConfigProvider struct {
	config *types.Config
	err    error
}

func (s *stubHostConfigProvider) GetEffectiveConfig(ctx context.Context) (*types.Config, error) {
	return s.config, s.err
}

func TestLoadFromHost_Defaults(t *testing.T) {
	ctx := types.WithHostConfigProvider(context.Background(), &stubHostConfigProvider{
		config: &types.Config{
			Logging: types.LoggingConfig{Level: "debug"},
			Plugins: types.PluginsConfig{},
		},
	})

	cfg, err := LoadFromHost(ctx, nil)
	if err != nil {
		t.Fatalf("LoadFromHost returned error: %v", err)
	}

	if cfg.Overwrite != true {
		t.Errorf("expected overwrite default true, got %v", cfg.Overwrite)
	}
	if cfg.Cleanup != false {
		t.Errorf("expected cleanup default false, got %v", cfg.Cleanup)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level to propagate, got %s", cfg.LogLevel)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("expected no default sources, got %v", cfg.Sources)
	}
}

func TestLoadFromHost_WithSettings(t *testing.T) {
	provider := &stubHostConfigProvider{
		config: &types.Config{
			Logging: types.LoggingConfig{Level: "info"},
			Plugins: types.PluginsConfig{
				Settings: map[string]map[string]interface{}{
					"s3": {
						"bucket":           "my-bucket",
						"region":           "us-east-2",
						"context_path":     "artifacts/build",
						"sources":          []interface{}{" ./dist ", "reports/output"},
						"cleanup":          true,
						"overwrite":        false,
						"endpoint":         "https://minio.internal",
						"force_path_style": true,
						"tls": map[string]interface{}{
							"skip_verify": true,
						},
						"credentials": map[string]interface{}{
							"access_key_id":     "abc",
							"secret_access_key": "xyz",
							"session_token":     "token",
						},
					},
				},
			},
		},
	}

	ctx := types.WithHostConfigProvider(context.Background(), provider)
	cfg, err := LoadFromHost(ctx, nil)
	if err != nil {
		t.Fatalf("LoadFromHost returned error: %v", err)
	}

	if cfg.Bucket != "my-bucket" {
		t.Errorf("expected bucket my-bucket, got %s", cfg.Bucket)
	}
	if cfg.Region != "us-east-2" {
		t.Errorf("expected region us-east-2, got %s", cfg.Region)
	}
	if cfg.ContextPath != "artifacts/build" {
		t.Errorf("expected context path artifacts/build, got %s", cfg.ContextPath)
	}
	if len(cfg.Sources) != 2 || cfg.Sources[0] != "./dist" || cfg.Sources[1] != "reports/output" {
		t.Errorf("expected sources to normalize, got %v", cfg.Sources)
	}
	if !cfg.Cleanup {
		t.Errorf("expected cleanup true")
	}
	if cfg.Overwrite {
		t.Errorf("expected overwrite false")
	}
	if cfg.Endpoint != "https://minio.internal" {
		t.Errorf("unexpected endpoint %s", cfg.Endpoint)
	}
	if !cfg.ForcePathStyle {
		t.Errorf("expected force path style true")
	}
	if !cfg.SkipTLSVerify {
		t.Errorf("expected tls skip verify true")
	}
	if cfg.Credentials.AccessKeyID != "abc" || cfg.Credentials.SecretAccessKey != "xyz" || cfg.Credentials.SessionToken != "token" {
		t.Errorf("credentials did not decode correctly: %+v", cfg.Credentials)
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := &Config{Bucket: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing bucket")
	}

	cfg = &Config{Bucket: "bucket", SkipTLSVerify: true}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when skip verify enabled without endpoint")
	}

	cfg = &Config{Bucket: "bucket", SkipTLSVerify: true, Endpoint: "https://example.com"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got %v", err)
	}
}
