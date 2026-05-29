// Package codingagent provides the Go port of @earendil-works/pi-coding-agent.
package codingagent

import (
	"context"
	"fmt"
	"os"
	"strings"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

const CurrentSessionVersion = core.CurrentSessionVersion

var (
	Version     = core.Version
	buildCommit string
	buildDate   string
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func Run(ctx context.Context, build BuildInfo, argv []string) error {
	return RunWithOptions(ctx, build, argv, MainOptions{})
}

type MainOptions struct {
	ExtensionFactories []ExtensionFactory
}

func RunWithOptions(ctx context.Context, build BuildInfo, argv []string, options MainOptions) error {
	SetBuildInfo(defaultBuildValue(build.Version, "dev"), defaultBuildValue(build.Commit, "none"), defaultBuildValue(build.Date, "unknown"))
	return MainWithOptions(ctx, argv, options)
}

func Main(ctx context.Context, argv []string) error {
	return MainWithOptions(ctx, argv, MainOptions{})
}

func MainWithOptions(ctx context.Context, argv []string, options MainOptions) error {
	if wantsVersion(argv) {
		fmt.Println(BuildVersion())
		return nil
	}
	if handled, err := handleConfigCommand(argv, os.Stdin, os.Stdout); handled {
		return err
	}
	if !skipsMigrations(argv) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd, _ = core.AbsPath(cwd)
		result, err := RunMigrations(cwd)
		if err != nil {
			return err
		}
		if err := ShowDeprecationWarnings(ctx, result.DeprecationWarnings); err != nil {
			return err
		}
	}
	return core.MainWithOptions(ctx, argv, core.MainOptions{ExtensionFactories: wrapExtensionFactories(options.ExtensionFactories)})
}

func SetBuildInfo(version, commit, date string) {
	if version != "" {
		Version = version
	}
	buildCommit = commit
	buildDate = date
}

func BuildVersion() string {
	if buildCommit == "" && buildDate == "" {
		return Version
	}
	parts := []string{Version}
	if buildCommit != "" {
		parts = append(parts, "commit "+buildCommit)
	}
	if buildDate != "" {
		parts = append(parts, "built "+buildDate)
	}
	return strings.Join(parts, " ")
}

func BuildCommit() string {
	return buildCommit
}

func BuildDate() string {
	return buildDate
}

func defaultBuildValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func wantsVersion(argv []string) bool {
	for _, arg := range argv {
		if arg == "--version" || arg == "-v" {
			return true
		}
	}
	return false
}

func skipsMigrations(argv []string) bool {
	if wantsHelp(argv) || len(argv) == 0 {
		return true
	}
	switch argv[0] {
	case "install", "remove", "uninstall", "update", "list", "config":
		return true
	default:
		return false
	}
}

func wantsHelp(argv []string) bool {
	for _, arg := range argv {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
