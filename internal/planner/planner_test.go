package planner

import (
	"os"
	"path/filepath"
	"testing"

	"dotfile-manager/internal/config"
	"dotfile-manager/internal/state"
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
	}, nil)
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
	}, &state.File{Items: []state.ManagedItem{{
		Path:        targetPath,
		Profile:     "plain_copy",
		Kind:        state.ItemFile,
		ContentHash: hashBytes(content),
		UID:         os.Geteuid(),
		GID:         os.Getegid(),
		Mode:        state.ModeFromUint32(0o644),
	}}})
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

func TestBuildOverwritesUnchangedManagedCopyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.conf")
	targetPath := filepath.Join(root, "target.conf")
	oldContent := []byte("foo=bar\n")
	newContent := []byte("foo=baz\n")
	if err := os.WriteFile(sourcePath, newContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Build(config.Resolved{Profiles: []config.ResolvedProfile{{
		Name:       "plain_copy",
		SourcePath: sourcePath,
		TargetPath: targetPath,
		Strategy:   config.StrategyCopy,
	}}}, &state.File{Items: []state.ManagedItem{{
		Path:        targetPath,
		Profile:     "plain_copy",
		Kind:        state.ItemFile,
		ContentHash: hashBytes(oldContent),
		UID:         os.Geteuid(),
		GID:         os.Getegid(),
		Mode:        state.ModeFromUint32(0o644),
	}}})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	if plan.Actions[0].Kind != ActionCopyFile || !plan.Actions[0].ExistingTarget {
		t.Fatalf("expected overwrite copy action, got %#v", plan.Actions[0])
	}
	if plan.Actions[0].ExpectedHash == "" {
		t.Fatal("expected managed overwrite to carry expected hash")
	}
}

func TestBuildErrorsOnModifiedManagedCopyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.conf")
	targetPath := filepath.Join(root, "target.conf")
	if err := os.WriteFile(sourcePath, []byte("foo=baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("foo=changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Build(config.Resolved{Profiles: []config.ResolvedProfile{{
		Name:       "plain_copy",
		SourcePath: sourcePath,
		TargetPath: targetPath,
		Strategy:   config.StrategyCopy,
	}}}, &state.File{Items: []state.ManagedItem{{
		Path:        targetPath,
		Profile:     "plain_copy",
		Kind:        state.ItemFile,
		ContentHash: hashBytes([]byte("foo=bar\n")),
		UID:         os.Geteuid(),
		GID:         os.Getegid(),
		Mode:        state.ModeFromUint32(0o644),
	}}})
	if err == nil {
		t.Fatal("expected modified managed copy file error")
	}
}

func TestBuildDoesNotCountExistingParentDirsAsSkipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourceDir := filepath.Join(root, "kitty")
	targetDir := filepath.Join(root, ".config", "kitty")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := Build(config.Resolved{Profiles: []config.ResolvedProfile{{
		Name:       "kitty",
		SourcePath: sourceDir,
		TargetPath: targetDir,
		Strategy:   config.StrategySymlink,
	}}}, nil)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	if plan.SkippedNoChange != 0 {
		t.Fatalf("expected 0 skipped items, got %d", plan.SkippedNoChange)
	}
}
