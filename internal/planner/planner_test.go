package planner

import (
	"os"
	"path/filepath"
	"testing"

	"dotfile-manager/internal/config"
)

func TestBuildRejectsNestedTargetConflicts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config_home")
	if err := os.MkdirAll(filepath.Join(configDir, "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "nvim", "init.lua"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "init.lua"), []byte("print('other')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Build(config.Resolved{
		Profiles: []config.ResolvedProfile{
			{
				Name:       "dir_profile",
				SourcePath: filepath.Join(configDir, "nvim"),
				TargetPath: filepath.Join(root, ".config", "nvim"),
				Strategy:   config.StrategySymlink,
			},
			{
				Name:       "file_profile",
				SourcePath: filepath.Join(configDir, "init.lua"),
				TargetPath: filepath.Join(root, ".config", "nvim", "init.lua"),
				Strategy:   config.StrategyCopy,
			},
		},
	})
	if err == nil {
		t.Fatal("expected nested target conflict error")
	}
}

func TestBuildSkipsUnchangedCopyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.conf")
	targetPath := filepath.Join(root, "target.conf")
	content := []byte("foo=bar\n")
	if err := os.WriteFile(sourcePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Build(config.Resolved{
		Profiles: []config.ResolvedProfile{
			{
				Name:       "plain_copy",
				SourcePath: sourcePath,
				TargetPath: targetPath,
				Strategy:   config.StrategyCopy,
			},
		},
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected no actions, got %d", len(plan.Actions))
	}
	if plan.SkippedNoChange == 0 {
		t.Fatal("expected unchanged file to be counted as skipped")
	}
}
