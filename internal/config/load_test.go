package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesNestedPathStructure(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	rootDir := filepath.Join(homeDir, "dotfile")
	targetDir := filepath.Join(homeDir, ".config")
	if err := os.MkdirAll(filepath.Join(rootDir, "config_home", "apps", "flags"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "config_home", "apps", "flags", "qq.conf"), []byte("flag=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(homeDir, "dotfile-mgr.yaml")
	content := []byte(`version: 1
root: "$HOME/dotfile"

groups:
  config_home:
    src: "config_home"
    dest: "$HOME/.config"
    strategy: copy

profiles:
  qq_flag:
    group: "config_home"
    path: "apps/flags/qq.conf"

hosts:
  default:
    enable:
      - "qq_flag"
  machine:
    enable: []
`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Load(LoadOptions{
		ConfigPath: configPath,
		Host:       "machine",
		Hostname:   "machine",
		HomeDir:    homeDir,
		Env:        map[string]string{},
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(resolved.Profiles) != 1 {
		t.Fatalf("expected 1 resolved profile, got %d", len(resolved.Profiles))
	}
	got := resolved.Profiles[0].TargetPath
	want := filepath.Join(targetDir, "apps", "flags", "qq.conf")
	if got != want {
		t.Fatalf("unexpected target path: got %q want %q", got, want)
	}
}
