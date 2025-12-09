package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/delivery-station/ds/pkg/types"
	"github.com/hashicorp/go-hclog"
	"github.com/mitchellh/mapstructure"
)

// Config captures the resolved plugin configuration.
type Config struct {
	Bucket         string
	Region         string
	ContextPath    string
	Sources        []string
	Cleanup        bool
	Overwrite      bool
	Endpoint       string
	ForcePathStyle bool
	SkipTLSVerify  bool
	Profile        string
	Credentials    Credentials
	LogLevel       string
}

// Credentials stores optional static credentials.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type rawSettings struct {
	Bucket         string   `mapstructure:"bucket"`
	Region         string   `mapstructure:"region"`
	ContextPath    string   `mapstructure:"context_path"`
	Sources        []string `mapstructure:"sources"`
	Cleanup        *bool    `mapstructure:"cleanup"`
	Overwrite      *bool    `mapstructure:"overwrite"`
	Endpoint       string   `mapstructure:"endpoint"`
	ForcePathStyle *bool    `mapstructure:"force_path_style"`
	Profile        string   `mapstructure:"profile"`
	TLS            *struct {
		SkipVerify *bool `mapstructure:"skip_verify"`
	} `mapstructure:"tls"`
	Credentials *struct {
		AccessKeyID     string `mapstructure:"access_key_id"`
		SecretAccessKey string `mapstructure:"secret_access_key"`
		SessionToken    string `mapstructure:"session_token"`
	} `mapstructure:"credentials"`
}

// LoadFromHost reads the plugin configuration from the DS host context.
func LoadFromHost(ctx context.Context, logger hclog.Logger) (*Config, error) {
	provider, ok := types.HostConfigFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("host configuration provider not available in context")
	}

	dsCfg, err := provider.GetEffectiveConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch host configuration: %w", err)
	}
	if dsCfg == nil {
		return nil, fmt.Errorf("host returned empty configuration payload")
	}

	settings := resolvePluginSettings(dsCfg.Plugins.Settings)

	pluginCfg, err := FromSettingsMap(settings)
	if err != nil {
		return nil, err
	}

	pluginCfg.LogLevel = strings.TrimSpace(dsCfg.Logging.Level)

	return pluginCfg, nil
}

// FromSettingsMap decodes a raw settings map into a Config applying defaults.
func FromSettingsMap(values map[string]interface{}) (*Config, error) {
	cfg := &Config{
		Cleanup:        false,
		Overwrite:      true,
		ForcePathStyle: false,
		SkipTLSVerify:  false,
	}

	if values == nil {
		return cfg, nil
	}

	raw := rawSettings{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:              "mapstructure",
		Result:               &raw,
		WeaklyTypedInput:     true,
		IgnoreUntaggedFields: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build settings decoder: %w", err)
	}

	if err := decoder.Decode(values); err != nil {
		return nil, fmt.Errorf("failed to decode plugin settings: %w", err)
	}

	cfg.Bucket = strings.TrimSpace(raw.Bucket)
	cfg.Region = strings.TrimSpace(raw.Region)
	cfg.ContextPath = normalizeContextPath(raw.ContextPath)
	cfg.Sources = normalizeSources(raw.Sources)
	cfg.Endpoint = strings.TrimSpace(raw.Endpoint)
	cfg.Profile = strings.TrimSpace(raw.Profile)

	if raw.Cleanup != nil {
		cfg.Cleanup = *raw.Cleanup
	}
	if raw.Overwrite != nil {
		cfg.Overwrite = *raw.Overwrite
	}
	if raw.ForcePathStyle != nil {
		cfg.ForcePathStyle = *raw.ForcePathStyle
	}
	if raw.TLS != nil && raw.TLS.SkipVerify != nil {
		cfg.SkipTLSVerify = *raw.TLS.SkipVerify
	}
	if raw.Credentials != nil {
		cfg.Credentials = Credentials{
			AccessKeyID:     strings.TrimSpace(raw.Credentials.AccessKeyID),
			SecretAccessKey: strings.TrimSpace(raw.Credentials.SecretAccessKey),
			SessionToken:    strings.TrimSpace(raw.Credentials.SessionToken),
		}
	}

	return cfg, nil
}

// Validate ensures essential values are present.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Bucket) == "" {
		return fmt.Errorf("bucket is required")
	}

	if c.SkipTLSVerify && strings.TrimSpace(c.Endpoint) == "" {
		return fmt.Errorf("tls.skip_verify can only be enabled when a custom endpoint is configured")
	}

	return nil
}

// Clone returns a shallow copy of the configuration.
func (c *Config) Clone() *Config {
	copyCfg := *c
	copyCfg.Credentials = c.Credentials
	if c.Sources != nil {
		copyCfg.Sources = append([]string{}, c.Sources...)
	}
	return &copyCfg
}

func normalizeContextPath(value string) string {
	trimmed := strings.TrimSpace(value)
	return strings.Trim(trimmed, "/")
}

func normalizeSources(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	cleaned := make([]string, 0, len(values))
	for _, candidate := range values {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}

	if len(cleaned) == 0 {
		return nil
	}

	return cleaned
}

func resolvePluginSettings(settings map[string]map[string]interface{}) map[string]interface{} {
	if settings == nil {
		return nil
	}

	for _, key := range []string{"s3", "ds-s3", "ds_s3"} {
		if cfg, ok := settings[key]; ok {
			return cfg
		}
	}

	return nil
}
