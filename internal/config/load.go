package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultConfigName = "dotfile-mgr.yaml"

func Locate(explicit string, homeDir string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("config file %q: %w", abs, err)
		}
		return abs, nil
	}

	paths := []string{
		filepath.Join(homeDir, ".config", "dotfile-manager", DefaultConfigName),
		filepath.Join(homeDir, "profile", DefaultConfigName),
	}

	for _, candidate := range paths {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("config file not found in default paths: %s, %s", paths[0], paths[1])
}

func Load(opts LoadOptions) (Resolved, error) {
	if opts.HomeDir == "" {
		return Resolved{}, errors.New("home directory is required")
	}
	if opts.Hostname == "" {
		return Resolved{}, errors.New("hostname is required")
	}
	if opts.Env == nil {
		opts.Env = map[string]string{}
	}

	configPath, err := Locate(opts.ConfigPath, opts.HomeDir)
	if err != nil {
		return Resolved{}, err
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return Resolved{}, fmt.Errorf("read config: %w", err)
	}

	var file File
	if err := yaml.Unmarshal(content, &file); err != nil {
		return Resolved{}, fmt.Errorf("parse config yaml: %w", err)
	}

	if err := validateFile(file); err != nil {
		return Resolved{}, err
	}

	hostName := opts.Host
	if hostName == "" {
		hostName = opts.Hostname
	}
	selected, ok := file.Hosts[hostName]
	if !ok {
		return Resolved{}, fmt.Errorf("host %q is not defined in config", hostName)
	}

	defaultHost := file.Hosts["default"]
	if len(defaultHost.HostProfiles) > 0 || len(defaultHost.Overrides) > 0 {
		return Resolved{}, errors.New("hosts.default only supports enable")
	}

	rootPath, err := expandAndAbs(file.Root, opts)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve root: %w", err)
	}
	if info, err := os.Stat(rootPath); err != nil {
		return Resolved{}, fmt.Errorf("root path %q: %w", rootPath, err)
	} else if !info.IsDir() {
		return Resolved{}, fmt.Errorf("root path %q is not a directory", rootPath)
	}

	if err := validateHostCollisions(file.Profiles, selected.HostProfiles); err != nil {
		return Resolved{}, err
	}

	enabled, err := mergeEnabled(defaultHost.Enable, selected.Enable)
	if err != nil {
		return Resolved{}, err
	}

	resolved := Resolved{
		ConfigPath: configPath,
		Host:       hostName,
		Profiles:   make([]ResolvedProfile, 0, len(enabled)),
	}

	for _, name := range enabled {
		profile, isHostProfile, err := lookupProfile(name, file.Profiles, selected.HostProfiles)
		if err != nil {
			return Resolved{}, err
		}

		override := selected.Overrides[name]
		resolvedProfile, err := resolveProfile(name, profile, override, file.Groups, rootPath, opts)
		if err != nil {
			return Resolved{}, err
		}
		_ = isHostProfile
		resolved.Profiles = append(resolved.Profiles, resolvedProfile)
	}

	if err := validateResolved(resolved); err != nil {
		return Resolved{}, err
	}

	return resolved, nil
}

func validateFile(file File) error {
	if file.Version != 1 {
		return fmt.Errorf("unsupported config version %d", file.Version)
	}
	if strings.TrimSpace(file.Root) == "" {
		return errors.New("root is required")
	}
	if len(file.Groups) == 0 {
		return errors.New("groups is required")
	}
	if len(file.Profiles) == 0 {
		return errors.New("profiles is required")
	}
	if len(file.Hosts) == 0 {
		return errors.New("hosts is required")
	}

	for name, group := range file.Groups {
		if !isSafeRelativePath(group.Src) {
			return fmt.Errorf("groups.%s.src must be a relative path", name)
		}
		if strings.TrimSpace(group.Dest) == "" {
			return fmt.Errorf("groups.%s.dest is required", name)
		}
		if err := validateStrategy(group.Strategy); err != nil {
			return fmt.Errorf("groups.%s.strategy: %w", name, err)
		}
		if group.Strategy == StrategyCopy && group.SymlinkForce != nil && *group.SymlinkForce {
			return fmt.Errorf("groups.%s.symlink_force is only valid for symlink strategies", name)
		}
		if group.Strategy != StrategyCopy && !group.Permissions.Empty() {
			return fmt.Errorf("groups.%s.permissions is only valid for copy strategy", name)
		}
		if err := validatePermissions(group.Permissions); err != nil {
			return fmt.Errorf("groups.%s.permissions: %w", name, err)
		}
	}

	for name, profile := range file.Profiles {
		if err := validateProfile(name, profile, file.Groups); err != nil {
			return err
		}
	}

	for name, host := range file.Hosts {
		if name == "default" && (len(host.HostProfiles) > 0 || len(host.Overrides) > 0) {
			return errors.New("hosts.default only supports enable")
		}
		if err := validateStringListUnique("hosts."+name+".enable", host.Enable); err != nil {
			return err
		}
		for profileName, profile := range host.HostProfiles {
			if _, exists := file.Profiles[profileName]; exists {
				return fmt.Errorf("host profile %q conflicts with global profile of the same name", profileName)
			}
			if err := validateProfile("hosts."+name+".host_profiles."+profileName, profile, file.Groups); err != nil {
				return err
			}
		}
		for overrideName, override := range host.Overrides {
			if _, exists := file.Profiles[overrideName]; !exists {
				if _, hostExists := host.HostProfiles[overrideName]; !hostExists {
					return fmt.Errorf("hosts.%s.overrides.%s references an unknown profile", name, overrideName)
				}
			}
			if override.Strategy != "" {
				if err := validateStrategy(override.Strategy); err != nil {
					return fmt.Errorf("hosts.%s.overrides.%s.strategy: %w", name, overrideName, err)
				}
			}
			if override.Strategy != StrategyCopy && override.Strategy != "" && !override.Permissions.Empty() {
				return fmt.Errorf("hosts.%s.overrides.%s.permissions is only valid for copy strategy", name, overrideName)
			}
			if err := validatePermissions(override.Permissions); err != nil {
				return fmt.Errorf("hosts.%s.overrides.%s.permissions: %w", name, overrideName, err)
			}
		}
	}

	return nil
}

func validateProfile(name string, profile Profile, groups map[string]Group) error {
	if strings.TrimSpace(profile.Group) == "" {
		return fmt.Errorf("%s.group is required", name)
	}
	group, ok := groups[profile.Group]
	if !ok {
		return fmt.Errorf("%s.group references unknown group %q", name, profile.Group)
	}
	if !isSafeRelativePath(profile.Path) {
		return fmt.Errorf("%s.path must be a relative path", name)
	}
	if profile.Strategy != "" {
		if err := validateStrategy(profile.Strategy); err != nil {
			return fmt.Errorf("%s.strategy: %w", name, err)
		}
	}
	strategy := profile.Strategy
	if strategy == "" {
		strategy = group.Strategy
	}
	if strategy == StrategyCopy && profile.SymlinkForce != nil && *profile.SymlinkForce {
		return fmt.Errorf("%s.symlink_force is only valid for symlink strategies", name)
	}
	if strategy != StrategyCopy && !profile.Permissions.Empty() {
		return fmt.Errorf("%s.permissions is only valid for copy strategy", name)
	}
	if err := validatePermissions(profile.Permissions); err != nil {
		return fmt.Errorf("%s.permissions: %w", name, err)
	}
	return nil
}

func validateResolved(resolved Resolved) error {
	if len(resolved.Profiles) == 0 {
		return errors.New("no enabled profiles")
	}
	for _, profile := range resolved.Profiles {
		if profile.Strategy == StrategySymlink && profile.ContentsOnly {
			return fmt.Errorf("profile %q: symlink does not support contents_only=true", profile.Name)
		}
		if profile.Strategy == StrategyCopy && profile.SymlinkForce {
			return fmt.Errorf("profile %q: symlink_force is only valid for symlink strategies", profile.Name)
		}
		if profile.Strategy != StrategyCopy && !profile.Permissions.Empty() {
			return fmt.Errorf("profile %q: permissions is only valid for copy strategy", profile.Name)
		}
	}
	return nil
}

func resolveProfile(name string, profile Profile, override Override, groups map[string]Group, rootPath string, opts LoadOptions) (ResolvedProfile, error) {
	group := groups[profile.Group]
	strategy := group.Strategy
	if profile.Strategy != "" {
		strategy = profile.Strategy
	}
	if override.Strategy != "" {
		strategy = override.Strategy
	}

	symlinkForce := false
	if group.SymlinkForce != nil {
		symlinkForce = *group.SymlinkForce
	}
	if profile.SymlinkForce != nil {
		symlinkForce = *profile.SymlinkForce
	}
	if override.SymlinkForce != nil {
		symlinkForce = *override.SymlinkForce
	}

	contentsOnly := false
	if profile.ContentsOnly != nil {
		contentsOnly = *profile.ContentsOnly
	}
	if override.ContentsOnly != nil {
		contentsOnly = *override.ContentsOnly
	}

	permissions := group.Permissions.Merge(profile.Permissions).Merge(override.Permissions)

	destRaw := group.Dest
	if profile.Dest != "" {
		destRaw = profile.Dest
	}
	if override.Dest != "" {
		destRaw = override.Dest
	}
	destPath, err := expandAndAbs(destRaw, opts)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("profile %q resolve dest: %w", name, err)
	}

	sourcePath := filepath.Join(rootPath, group.Src, filepath.Clean(profile.Path))
	targetPath := filepath.Join(destPath, filepath.Clean(profile.Path))
	if contentsOnly {
		targetPath = destPath
	}

	return ResolvedProfile{
		Name:         name,
		Group:        profile.Group,
		SourcePath:   sourcePath,
		TargetPath:   targetPath,
		Strategy:     strategy,
		ContentsOnly: contentsOnly,
		SymlinkForce: symlinkForce,
		Permissions:  permissions,
	}, nil
}

func lookupProfile(name string, global, host map[string]Profile) (Profile, bool, error) {
	_, globalExists := global[name]
	_, hostExists := host[name]
	if globalExists && hostExists {
		return Profile{}, false, fmt.Errorf("profile %q is defined in both profiles and host_profiles", name)
	}
	if globalExists {
		return global[name], false, nil
	}
	if hostExists {
		return host[name], true, nil
	}
	return Profile{}, false, fmt.Errorf("enabled profile %q is not defined", name)
}

func mergeEnabled(defaultEnabled []string, hostEnabled []string) ([]string, error) {
	result := make([]string, 0, len(defaultEnabled)+len(hostEnabled))
	seen := map[string]struct{}{}
	for _, name := range append(append([]string{}, defaultEnabled...), hostEnabled...) {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("enable contains an empty profile name")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func validateHostCollisions(global map[string]Profile, host map[string]Profile) error {
	for name := range host {
		if _, exists := global[name]; exists {
			return fmt.Errorf("host_profiles.%s conflicts with global profile of the same name", name)
		}
	}
	return nil
}

func validateStrategy(strategy Strategy) error {
	switch strategy {
	case StrategySymlink, StrategyRecursiveSymlink, StrategyCopy:
		return nil
	default:
		return fmt.Errorf("unsupported strategy %q", strategy)
	}
}

func validatePermissions(permissions Permissions) error {
	if permissions.FileMode != "" {
		if _, err := parseModeString(permissions.FileMode); err != nil {
			return err
		}
	}
	if permissions.DirMode != "" {
		if _, err := parseModeString(permissions.DirMode); err != nil {
			return err
		}
	}
	return nil
}

func validateStringListUnique(name string, values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s contains duplicate entry %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func parseModeString(raw string) (os.FileMode, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	for _, r := range raw {
		if r < '0' || r > '7' {
			return 0, fmt.Errorf("invalid file mode %q", raw)
		}
	}
	var mode uint64
	for _, r := range raw {
		mode = mode*8 + uint64(r-'0')
	}
	return os.FileMode(mode), nil
}

func expandAndAbs(raw string, opts LoadOptions) (string, error) {
	expanded := os.Expand(raw, func(key string) string {
		if key == "HOME" {
			return opts.HomeDir
		}
		if value, ok := opts.Env[key]; ok {
			return value
		}
		return os.Getenv(key)
	})
	return filepath.Abs(expanded)
}

func isSafeRelativePath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return false
	}
	return cleaned != ".." && !strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func SortedProfileNames(resolved Resolved) []string {
	names := make([]string, 0, len(resolved.Profiles))
	for _, profile := range resolved.Profiles {
		names = append(names, profile.Name)
	}
	sort.Strings(names)
	return names
}
