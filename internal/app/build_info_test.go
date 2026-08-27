package app

import "testing"

func TestCurrentBuildFingerprintHasSafeIdentityFields(t *testing.T) {
	fingerprint := CurrentBuildFingerprint()
	if fingerprint.RepositoryName != coreRepositoryName {
		t.Fatalf("repository name = %q", fingerprint.RepositoryName)
	}
	if fingerprint.BinaryVersion == "" || fingerprint.GitCommitSHA == "" ||
		fingerprint.BuildTimestamp == "" || fingerprint.DirtyBuild == "" ||
		fingerprint.RuntimeEnvironment == "" {
		t.Fatalf("incomplete build fingerprint: %+v", fingerprint)
	}
}

func TestProductionBuildFingerprintValidation(t *testing.T) {
	valid := BuildFingerprint{
		RepositoryName: coreRepositoryName, BinaryVersion: "main-42",
		GitCommitSHA:   "0123456789abcdef0123456789abcdef01234567",
		BuildTimestamp: "2026-08-02T12:34:56Z", DirtyBuild: "false",
		RuntimeEnvironment: "production",
	}
	if err := ValidateProductionBuildFingerprint(valid); err != nil {
		t.Fatalf("valid production fingerprint rejected: %v", err)
	}
	mutations := map[string]BuildFingerprint{
		"development version": func() BuildFingerprint { value := valid; value.BinaryVersion = "dev"; return value }(),
		"missing sha":         func() BuildFingerprint { value := valid; value.GitCommitSHA = "unknown"; return value }(),
		"invalid timestamp":   func() BuildFingerprint { value := valid; value.BuildTimestamp = "unknown"; return value }(),
		"invalid dirty flag":  func() BuildFingerprint { value := valid; value.DirtyBuild = "unknown"; return value }(),
	}
	for name, fingerprint := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProductionBuildFingerprint(fingerprint); err == nil {
				t.Fatalf("invalid production fingerprint accepted: %+v", fingerprint)
			}
		})
	}
}
