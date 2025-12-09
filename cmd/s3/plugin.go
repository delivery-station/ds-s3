package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/delivery-station/ds-s3/internal/config"
	"github.com/delivery-station/ds-s3/internal/uploader"
	"github.com/delivery-station/ds/pkg/types"
	"github.com/hashicorp/go-hclog"
)

// Plugin implements the DS PluginProtocol for ds-s3.
type Plugin struct {
	logger  hclog.Logger
	version string
	commit  string
	date    string
}

// NewPlugin constructs a Plugin instance.
func NewPlugin(logger hclog.Logger, version, commit, date string) *Plugin {
	return &Plugin{
		logger:  logger,
		version: version,
		commit:  commit,
		date:    date,
	}
}

func (p *Plugin) GetMetadata(ctx context.Context) (*types.PluginMetadata, error) {
	return &types.PluginMetadata{
		Name:        "s3",
		Version:     p.version,
		Description: "Upload artifacts to S3-compatible storage",
		Operations:  []string{"upload", "help", "version"},
		Platform: types.PluginPlatform{
			OS:   []string{"linux", "darwin", "windows"},
			Arch: []string{"amd64", "arm64"},
		},
	}, nil
}

func (p *Plugin) Execute(ctx context.Context, operation string, args []string, env map[string]string) (*types.ExecutionResult, error) {
	// Adopt environment variables passed by DS
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return &types.ExecutionResult{ExitCode: 1, Error: fmt.Sprintf("failed to set environment variable %s: %v", k, err)}, nil
		}
	}

	cfg, err := config.LoadFromHost(ctx, p.logger)
	if err != nil {
		p.logger.Error("Failed to load configuration from host", "error", err)
		return &types.ExecutionResult{ExitCode: 1, Error: err.Error()}, nil
	}

	if level := strings.TrimSpace(cfg.LogLevel); level != "" {
		if parsed := hclog.LevelFromString(level); parsed != hclog.NoLevel {
			p.logger.SetLevel(parsed)
		}
	}

	switch operation {
	case "upload":
		return p.handleUpload(ctx, cfg, args)
	case "help":
		return &types.ExecutionResult{
			Stdout:   uploadUsage(),
			ExitCode: 0,
		}, nil
	case "version":
		return &types.ExecutionResult{
			Stdout:   fmt.Sprintf("ds-s3 version %s\n  commit: %s\n  built:  %s\n", p.version, p.commit, p.date),
			ExitCode: 0,
		}, nil
	default:
		return &types.ExecutionResult{ExitCode: 1, Error: fmt.Sprintf("unknown operation: %s", operation)}, nil
	}
}

func (p *Plugin) ValidateConfig(ctx context.Context, raw map[string]interface{}) error {
	cfg, err := config.FromSettingsMap(raw)
	if err != nil {
		return err
	}
	return cfg.Validate()
}

func (p *Plugin) GetSchema(ctx context.Context) (*types.PluginSchema, error) {
	return &types.PluginSchema{
		Version: "1.0.0",
		Properties: map[string]types.SchemaProperty{
			"bucket": {
				Type:        "string",
				Description: "Target S3 bucket name",
				Required:    true,
			},
			"region": {
				Type:        "string",
				Description: "AWS region for the bucket (falls back to AWS SDK defaults)",
			},
			"context_path": {
				Type:        "string",
				Description: "Prefix under which objects are stored",
			},
			"sources": {
				Type:        "array",
				Description: "Default source paths used when no CLI paths are supplied",
			},
			"cleanup": {
				Type:        "boolean",
				Description: "Remove existing objects beneath the context path before uploading",
				Default:     "false",
			},
			"overwrite": {
				Type:        "boolean",
				Description: "Overwrite objects when they already exist",
				Default:     "true",
			},
			"endpoint": {
				Type:        "string",
				Description: "Custom S3-compatible endpoint URL",
			},
			"force_path_style": {
				Type:        "boolean",
				Description: "Use path-style addressing (required by some providers like MinIO)",
				Default:     "false",
			},
			"tls.skip_verify": {
				Type:        "boolean",
				Description: "Disable TLS verification when using a custom endpoint",
				Default:     "false",
			},
			"profile": {
				Type:        "string",
				Description: "Shared AWS credentials profile name",
			},
			"credentials.access_key_id": {
				Type:        "string",
				Description: "AWS access key ID override",
			},
			"credentials.secret_access_key": {
				Type:        "string",
				Description: "AWS secret access key override",
			},
			"credentials.session_token": {
				Type:        "string",
				Description: "AWS session token override",
			},
		},
	}, nil
}

func (p *Plugin) handleUpload(ctx context.Context, baseCfg *config.Config, args []string) (*types.ExecutionResult, error) {
	if len(args) > 0 {
		first := strings.TrimSpace(args[0])
		if first == "-h" || first == "--help" || first == "help" {
			return &types.ExecutionResult{Stdout: uploadUsage(), ExitCode: 0}, nil
		}
	}

	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.Usage = func() {
		buf.WriteString(uploadUsage())
	}

	bucket := fs.String("bucket", "", "Target S3 bucket")
	region := fs.String("region", "", "AWS region to use")
	contextPath := fs.String("context", "", "Context path/prefix to apply")
	cleanup := fs.Bool("cleanup", baseCfg.Cleanup, "Remove existing objects before upload")
	overwrite := fs.Bool("overwrite", baseCfg.Overwrite, "Overwrite objects when they already exist")
	endpoint := fs.String("endpoint", "", "Custom S3-compatible endpoint URL")
	forcePathStyle := fs.Bool("force-path-style", baseCfg.ForcePathStyle, "Force path-style addressing")
	skipTLSVerify := fs.Bool("skip-tls-verify", baseCfg.SkipTLSVerify, "Disable TLS certificate verification")
	profile := fs.String("profile", "", "Shared credentials profile to load")

	if err := fs.Parse(args); err != nil {
		return &types.ExecutionResult{ExitCode: 1, Stderr: buf.String(), Error: err.Error()}, nil
	}

	merged := baseCfg.Clone()

	sources := fs.Args()
	if len(sources) == 0 {
		sources = append([]string{}, merged.Sources...)
	}
	if len(sources) == 0 {
		err := fmt.Errorf("at least one source path is required (provide CLI paths or configure sources)")
		return &types.ExecutionResult{ExitCode: 1, Stderr: uploadUsage(), Error: err.Error()}, nil
	}
	if *bucket != "" {
		merged.Bucket = *bucket
	}
	if *region != "" {
		merged.Region = *region
	}
	if *contextPath != "" {
		merged.ContextPath = strings.Trim(*contextPath, "/")
	}
	if *endpoint != "" {
		merged.Endpoint = *endpoint
	}
	if *profile != "" {
		merged.Profile = *profile
	}
	merged.Cleanup = *cleanup
	merged.Overwrite = *overwrite
	merged.ForcePathStyle = *forcePathStyle
	merged.SkipTLSVerify = *skipTLSVerify

	if err := merged.Validate(); err != nil {
		return &types.ExecutionResult{ExitCode: 1, Error: err.Error()}, nil
	}

	awsCfg, err := p.buildAWSConfig(ctx, merged)
	if err != nil {
		return &types.ExecutionResult{ExitCode: 1, Error: fmt.Sprintf("failed to configure AWS SDK: %v", err)}, nil
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = merged.ForcePathStyle
		if merged.Endpoint != "" {
			o.BaseEndpoint = aws.String(merged.Endpoint)
			o.Region = awsCfg.Region
		}
	})
	transfer := uploader.NewTransport(client, manager.NewUploader(client), merged.Bucket, merged.Overwrite)

	plans, err := uploader.BuildPlans(sources, merged.ContextPath)
	if err != nil {
		return &types.ExecutionResult{ExitCode: 1, Error: err.Error()}, nil
	}

	cleaned := 0
	if merged.Cleanup {
		deleted, err := transfer.Cleanup(ctx, merged.ContextPath)
		if err != nil {
			return &types.ExecutionResult{ExitCode: 1, Error: fmt.Sprintf("cleanup failed: %v", err)}, nil
		}
		cleaned = deleted
		p.logger.Info("Cleanup completed", "deleted", deleted, "prefix", merged.ContextPath)
	}

	results, err := transfer.Upload(ctx, plans)
	if err != nil {
		return &types.ExecutionResult{ExitCode: 1, Error: err.Error()}, nil
	}

	summary := uploadSummary{
		Bucket:          merged.Bucket,
		Region:          merged.Region,
		ContextPath:     merged.ContextPath,
		CleanupEnabled:  merged.Cleanup,
		ObjectsRemoved:  cleaned,
		ObjectsUploaded: results,
	}

	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return &types.ExecutionResult{ExitCode: 1, Error: fmt.Sprintf("failed to encode execution summary: %v", err)}, nil
	}

	return &types.ExecutionResult{
		Stdout:   string(payload) + "\n",
		ExitCode: 0,
	}, nil
}

func (p *Plugin) buildAWSConfig(ctx context.Context, cfg *config.Config) (aws.Config, error) {
	options := make([]func(*awsconfig.LoadOptions) error, 0)
	if cfg.Region != "" {
		options = append(options, awsconfig.WithRegion(cfg.Region))
	}
	if cfg.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.SkipTLSVerify {
		transport := &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 - explicitly requested by user configuration
		}
		options = append(options, awsconfig.WithHTTPClient(&http.Client{Transport: transport}))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return aws.Config{}, err
	}

	if cfg.Region != "" {
		awsCfg.Region = cfg.Region
	}
	if awsCfg.Region == "" {
		awsCfg.Region = "us-east-1"
	}

	if cfg.Credentials.AccessKeyID != "" && cfg.Credentials.SecretAccessKey != "" {
		awsCfg.Credentials = aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			cfg.Credentials.AccessKeyID,
			cfg.Credentials.SecretAccessKey,
			cfg.Credentials.SessionToken,
		))
	}

	return awsCfg, nil
}

func uploadUsage() string {
	return `Usage: ds s3 upload [flags] <path> [path...]

Uploads one or more files/directories to an S3-compatible bucket.

Flags:
  --bucket <name>            Override target bucket (defaults to configuration)
  --region <name>            Override AWS region
  --context <prefix>         Set object prefix/context path
  --cleanup                  Remove existing objects before uploading
  --overwrite                Overwrite conflicting objects (default true)
  --endpoint <url>           Use a custom S3-compatible endpoint
  --force-path-style         Force path-style addressing
  --skip-tls-verify          Disable TLS verification (requires --endpoint)
  --profile <name>           Shared AWS profile to use
`
}

type uploadSummary struct {
	Bucket          string                  `json:"bucket"`
	Region          string                  `json:"region,omitempty"`
	ContextPath     string                  `json:"context_path,omitempty"`
	CleanupEnabled  bool                    `json:"cleanup_enabled"`
	ObjectsRemoved  int                     `json:"objects_removed"`
	ObjectsUploaded []uploader.UploadResult `json:"objects_uploaded"`
}
