package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposePassesTreeAuditConfigurationToAPI(t *testing.T) {
	path := filepath.Join("..", "..", "compose.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	compose := string(content)
	want := []string{
		"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT: ${AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT:-}",
		"AZURE_OPENAI_FINAL_TREE_REVIEW_DEPLOYMENT: ${AZURE_OPENAI_FINAL_TREE_REVIEW_DEPLOYMENT:-}",
		"TREE_AUDIT_ENABLED: ${TREE_AUDIT_ENABLED:-false}",
		"TREE_AUDIT_MODE: ${TREE_AUDIT_MODE:-shadow}",
		"TREE_AUDIT_INTERVAL_VERSIONS: ${TREE_AUDIT_INTERVAL_VERSIONS:-3}",
		"TREE_AUDIT_INTERVAL_SECONDS: ${TREE_AUDIT_INTERVAL_SECONDS:-300}",
		"TREE_AUDIT_MIN_INTERVAL_SECONDS: ${TREE_AUDIT_MIN_INTERVAL_SECONDS:-300}",
		"TREE_AUDIT_MAX_RUNS_PER_SESSION: ${TREE_AUDIT_MAX_RUNS_PER_SESSION:-20}",
		"TREE_AUDIT_MAX_RUNS_PER_HOUR: ${TREE_AUDIT_MAX_RUNS_PER_HOUR:-12}",
		"TREE_AUDIT_HIGH_SEVERITY_MIN_INTERVAL_SECONDS: ${TREE_AUDIT_HIGH_SEVERITY_MIN_INTERVAL_SECONDS:-60}",
		"TREE_AUDIT_HIGH_SEVERITY_MAX_RUNS_PER_HOUR: ${TREE_AUDIT_HIGH_SEVERITY_MAX_RUNS_PER_HOUR:-4}",
	}
	for _, item := range want {
		if !strings.Contains(compose, item) {
			t.Errorf("compose api environment is missing %q", item)
		}
	}
}
