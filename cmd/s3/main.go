package main

import (
	"fmt"
	"os"

	pkgplugin "github.com/delivery-station/ds/pkg/plugin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			printVersion()
			return
		case "-help", "--help", "help":
			printStandaloneHelp()
			return
		}
	}

	logger := hclog.New(&hclog.LoggerOptions{
		Name:       "ds-s3",
		Output:     os.Stderr,
		Level:      hclog.Info,
		JSONFormat: true,
	})

	s3Plugin := NewPlugin(logger, version, commit, date)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pkgplugin.Handshake,
		Plugins: map[string]plugin.Plugin{
			"ds-plugin": &pkgplugin.DSPlugin{Impl: s3Plugin},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}

func printVersion() {
	lines := []string{
		fmt.Sprintf("ds-s3 version %s", version),
		fmt.Sprintf("  commit: %s", commit),
		fmt.Sprintf("  built:  %s", date),
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stdout, line)
	}
}

func printStandaloneHelp() {
	lines := []string{
		"ds-s3 is a Delivery Station plugin and must be launched by DS.",
		"Usage: ds s3 <command> [args]",
		"Commands:",
		"  upload   Upload local files or directories to an S3-compatible bucket",
		"  help     Show this help message",
		"  version  Show plugin version metadata",
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stdout, line)
	}
}
