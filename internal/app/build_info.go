package app

import (
	"fmt"
	"log"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

const coreRepositoryName = "deciscope-core-api"

var productionCommitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// These values are injected by the release build. debug.ReadBuildInfo supplies
// a safe fallback for local go build binaries built from a VCS checkout.
var (
	binaryVersion  = "dev"
	gitCommitSHA   = ""
	buildTimestamp = ""
	dirtyBuild     = ""
)

type BuildFingerprint struct {
	RepositoryName     string
	BinaryVersion      string
	GitCommitSHA       string
	BuildTimestamp     string
	DirtyBuild         string
	RuntimeEnvironment string
}

func CurrentBuildFingerprint() BuildFingerprint {
	fingerprint := BuildFingerprint{
		RepositoryName:     coreRepositoryName,
		BinaryVersion:      normalizedBuildValue(binaryVersion, "dev"),
		GitCommitSHA:       normalizedBuildValue(gitCommitSHA, "unknown"),
		BuildTimestamp:     normalizedBuildValue(buildTimestamp, "unknown"),
		DirtyBuild:         normalizedDirtyBuild(dirtyBuild),
		RuntimeEnvironment: environmentFromEnv(),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if fingerprint.BinaryVersion == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			fingerprint.BinaryVersion = normalizedBuildValue(info.Main.Version, "dev")
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if fingerprint.GitCommitSHA == "unknown" {
					fingerprint.GitCommitSHA = normalizedBuildValue(setting.Value, "unknown")
				}
			case "vcs.modified":
				if fingerprint.DirtyBuild == "unknown" {
					fingerprint.DirtyBuild = normalizedDirtyBuild(setting.Value)
				}
			}
		}
	}
	return fingerprint
}

func LogBuildFingerprint() {
	fingerprint := CurrentBuildFingerprint()
	log.Printf(
		"build fingerprint repositoryName=%q binaryVersion=%q gitCommitSha=%q buildTimestamp=%q dirtyBuild=%q runtimeEnvironment=%q",
		fingerprint.RepositoryName,
		fingerprint.BinaryVersion,
		fingerprint.GitCommitSHA,
		fingerprint.BuildTimestamp,
		fingerprint.DirtyBuild,
		fingerprint.RuntimeEnvironment,
	)
}

// ValidateProductionBuildFingerprint is shared by release checks and keeps
// local development permissive while making a production identity auditable.
func ValidateProductionBuildFingerprint(fingerprint BuildFingerprint) error {
	if fingerprint.RepositoryName != coreRepositoryName {
		return fmt.Errorf("unexpected repository name %q", fingerprint.RepositoryName)
	}
	version := strings.ToLower(strings.TrimSpace(fingerprint.BinaryVersion))
	if version == "" || version == "dev" || version == "unknown" {
		return fmt.Errorf("production binary version is not injected")
	}
	if !productionCommitSHA.MatchString(strings.TrimSpace(fingerprint.GitCommitSHA)) {
		return fmt.Errorf("production git commit SHA is invalid")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(fingerprint.BuildTimestamp)); err != nil {
		return fmt.Errorf("production build timestamp is invalid: %w", err)
	}
	if _, err := strconv.ParseBool(strings.TrimSpace(fingerprint.DirtyBuild)); err != nil {
		return fmt.Errorf("production dirty-build flag is invalid: %w", err)
	}
	return nil
}

func normalizedBuildValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return strings.Join(strings.Fields(value), "_")
}

func normalizedDirtyBuild(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := strconv.ParseBool(value); err == nil {
		return strconv.FormatBool(parsed)
	}
	return "unknown"
}
