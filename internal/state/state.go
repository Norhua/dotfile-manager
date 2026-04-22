package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Version = 1

type OctalMode string

func ModeFromUint32(mode uint32) OctalMode {
	return OctalMode(fmt.Sprintf("%04o", mode))
}

func (m OctalMode) Uint32() (uint32, error) {
	if m == "" {
		return 0, nil
	}
	var value uint32
	for _, r := range string(m) {
		if r < '0' || r > '7' {
			return 0, fmt.Errorf("invalid octal mode %q", string(m))
		}
		value = value*8 + uint32(r-'0')
	}
	return value, nil
}

func (m OctalMode) MustUint32() uint32 {
	value, err := m.Uint32()
	if err != nil {
		panic(err)
	}
	return value
}

func (m *OctalMode) UnmarshalJSON(data []byte) error {
	var stringValue string
	if err := json.Unmarshal(data, &stringValue); err == nil {
		parsed := OctalMode(strings.TrimSpace(stringValue))
		if _, err := parsed.Uint32(); err != nil {
			return err
		}
		*m = parsed
		return nil
	}

	var numericValue uint32
	if err := json.Unmarshal(data, &numericValue); err == nil {
		*m = ModeFromUint32(numericValue)
		return nil
	}

	return fmt.Errorf("invalid mode json value %s", string(data))
}

func (m OctalMode) MarshalJSON() ([]byte, error) {
	if _, err := m.Uint32(); err != nil {
		return nil, err
	}
	return json.Marshal(string(m))
}

type ItemKind string

const (
	ItemFile    ItemKind = "file"
	ItemDir     ItemKind = "dir"
	ItemSymlink ItemKind = "symlink"
)

type ManagedItem struct {
	Path          string    `json:"path"`
	Profile       string    `json:"profile"`
	Kind          ItemKind  `json:"kind"`
	Strategy      string    `json:"strategy"`
	LinkTarget    string    `json:"link_target,omitempty"`
	ContentHash   string    `json:"content_hash,omitempty"`
	UID           int       `json:"uid"`
	GID           int       `json:"gid"`
	Mode          OctalMode `json:"mode"`
	CreatedByTool bool      `json:"created_by_tool"`
}

type File struct {
	Version    int           `json:"version"`
	ConfigPath string        `json:"config_path"`
	Host       string        `json:"host"`
	Items      []ManagedItem `json:"items"`
}

func Directory(homeDir string, env map[string]string) string {
	if value := strings.TrimSpace(env["DOTFILE_MANAGER_STATE_DIR"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(env["XDG_STATE_HOME"]); value != "" {
		return filepath.Join(value, "dotfile-manager")
	}
	return filepath.Join(homeDir, ".local", "state", "dotfile-manager")
}

func Path(configPath string, host string, homeDir string, env map[string]string) string {
	hasher := sha256.Sum256([]byte(filepath.Clean(configPath)))
	hashValue := hex.EncodeToString(hasher[:])[:16]
	return filepath.Join(Directory(homeDir, env), hashValue, host+".json")
}

func Load(path string) (File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(content, &file); err != nil {
		return File{}, err
	}
	if file.Version != Version {
		return File{}, fmt.Errorf("unsupported state version %d", file.Version)
	}
	return file, nil
}

func Save(path string, file File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file.Version = Version
	sort.Slice(file.Items, func(i, j int) bool {
		return file.Items[i].Path < file.Items[j].Path
	})
	content, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (f File) ItemMap() map[string]ManagedItem {
	result := make(map[string]ManagedItem, len(f.Items))
	for _, item := range f.Items {
		result[item.Path] = item
	}
	return result
}
