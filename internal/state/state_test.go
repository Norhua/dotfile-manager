package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOctalModeMarshalsAsString(t *testing.T) {
	t.Parallel()

	file := File{
		Version:    Version,
		ConfigPath: "/tmp/config.yaml",
		Host:       "host",
		Items: []ManagedItem{{
			Path:    "/tmp/file",
			Profile: "demo",
			Kind:    ItemFile,
			Mode:    ModeFromUint32(0o600),
		}},
	}

	content, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if !strings.Contains(string(content), `"mode":"0600"`) {
		t.Fatalf("expected octal string mode, got %s", string(content))
	}
}

func TestOctalModeUnmarshalsLegacyNumericMode(t *testing.T) {
	t.Parallel()

	content := []byte(`{
		"version": 1,
		"config_path": "/tmp/config.yaml",
		"host": "host",
		"items": [{
			"path": "/tmp/file",
			"profile": "demo",
			"kind": "file",
			"strategy": "copy",
			"mode": 384,
			"uid": 0,
			"gid": 0,
			"created_by_tool": true
		}]
	}`)

	var file File
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatalf("unmarshal legacy state: %v", err)
	}
	if got := string(file.Items[0].Mode); got != "0600" {
		t.Fatalf("expected legacy numeric mode to convert to 0600, got %q", got)
	}
}
