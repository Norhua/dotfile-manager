package config

import "encoding/json"

type Strategy string

const (
	StrategySymlink          Strategy = "symlink"
	StrategyRecursiveSymlink Strategy = "recursive_symlink"
	StrategyCopy             Strategy = "copy"
)

type Permissions struct {
	Owner    string `yaml:"owner" json:"owner"`
	FileMode string `yaml:"file_mode" json:"file_mode"`
	DirMode  string `yaml:"dir_mode" json:"dir_mode"`
}

func (p Permissions) Empty() bool {
	return p.Owner == "" && p.FileMode == "" && p.DirMode == ""
}

func (p Permissions) Merge(other Permissions) Permissions {
	merged := p
	if other.Owner != "" {
		merged.Owner = other.Owner
	}
	if other.FileMode != "" {
		merged.FileMode = other.FileMode
	}
	if other.DirMode != "" {
		merged.DirMode = other.DirMode
	}
	return merged
}

type Group struct {
	Src          string      `yaml:"src"`
	Dest         string      `yaml:"dest"`
	Strategy     Strategy    `yaml:"strategy"`
	SymlinkForce *bool       `yaml:"symlink_force"`
	Permissions  Permissions `yaml:"permissions"`
}

type Profile struct {
	Group        string      `yaml:"group"`
	Path         string      `yaml:"path"`
	Dest         string      `yaml:"dest"`
	Strategy     Strategy    `yaml:"strategy"`
	ContentsOnly *bool       `yaml:"contents_only"`
	SymlinkForce *bool       `yaml:"symlink_force"`
	Permissions  Permissions `yaml:"permissions"`
}

type Override struct {
	Dest         string      `yaml:"dest"`
	Strategy     Strategy    `yaml:"strategy"`
	ContentsOnly *bool       `yaml:"contents_only"`
	SymlinkForce *bool       `yaml:"symlink_force"`
	Permissions  Permissions `yaml:"permissions"`
}

type Host struct {
	Enable       []string            `yaml:"enable"`
	HostProfiles map[string]Profile  `yaml:"host_profiles"`
	Overrides    map[string]Override `yaml:"overrides"`
}

type File struct {
	Version  int                `yaml:"version"`
	Root     string             `yaml:"root"`
	Groups   map[string]Group   `yaml:"groups"`
	Profiles map[string]Profile `yaml:"profiles"`
	Hosts    map[string]Host    `yaml:"hosts"`
}

type ResolvedProfile struct {
	Name         string      `json:"name"`
	Group        string      `json:"group"`
	SourcePath   string      `json:"source_path"`
	TargetPath   string      `json:"target_path"`
	Strategy     Strategy    `json:"strategy"`
	ContentsOnly bool        `json:"contents_only"`
	SymlinkForce bool        `json:"symlink_force"`
	Permissions  Permissions `json:"permissions"`
}

type Resolved struct {
	ConfigPath string            `json:"config_path"`
	Host       string            `json:"host"`
	Profiles   []ResolvedProfile `json:"profiles"`
}

func (r Resolved) MarshalJSON() ([]byte, error) {
	type alias Resolved
	return json.Marshal(alias(r))
}

type LoadOptions struct {
	ConfigPath string
	Host       string
	Hostname   string
	Env        map[string]string
	HomeDir    string
}
