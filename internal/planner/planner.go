package planner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"dotfile-manager/internal/config"
	"dotfile-manager/internal/state"

	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/sys/unix"
)

const (
	ActionEnsureDir        ActionKind = "ensure_dir"
	ActionSymlink          ActionKind = "symlink"
	ActionCopyFile         ActionKind = "copy_file"
	ActionRemoveFile       ActionKind = "remove_file"
	ActionRemoveSymlink    ActionKind = "remove_symlink"
	ActionRemoveDirIfEmpty ActionKind = "remove_dir_if_empty"
)

const (
	diffMaxBytes = 128 * 1024
	diffMaxLines = 400
)

type ActionKind string

type Action struct {
	Kind               ActionKind `json:"kind"`
	Profile            string     `json:"profile"`
	StateKind          string     `json:"state_kind,omitempty"`
	SourcePath         string     `json:"source_path,omitempty"`
	TargetPath         string     `json:"target_path"`
	AutoParent         bool       `json:"auto_parent"`
	ExistingTarget     bool       `json:"existing_target"`
	Replace            bool       `json:"replace"`
	ReplaceRecursive   bool       `json:"replace_recursive"`
	MetadataOnly       bool       `json:"metadata_only"`
	ContentChanged     bool       `json:"content_changed"`
	RequiresPrivilege  bool       `json:"requires_privilege"`
	ManageOwner        bool       `json:"manage_owner"`
	DesiredUID         int        `json:"desired_uid"`
	DesiredGID         int        `json:"desired_gid"`
	OwnerLabel         string     `json:"owner_label,omitempty"`
	ManageMode         bool       `json:"manage_mode"`
	DesiredMode        uint32     `json:"desired_mode"`
	Diff               string     `json:"diff,omitempty"`
	DiffSummary        string     `json:"diff_summary,omitempty"`
	ManagedPathIsDir   bool       `json:"managed_path_is_dir"`
	SourceOwnerLabel   string     `json:"source_owner_label,omitempty"`
	SourceModeOctal    string     `json:"source_mode_octal,omitempty"`
	DesiredModeOctal   string     `json:"desired_mode_octal,omitempty"`
	DesiredTargetLabel string     `json:"desired_target_label,omitempty"`
	ExpectedHash       string     `json:"expected_hash,omitempty"`
	ExpectedLinkTarget string     `json:"expected_link_target,omitempty"`
	SourceHash         string     `json:"source_hash,omitempty"`
	TrackState         bool       `json:"track_state"`
	CreatedByTool      bool       `json:"created_by_tool"`
}

type Plan struct {
	Actions                    []Action            `json:"actions"`
	ObservedItems              []state.ManagedItem `json:"observed_items,omitempty"`
	Warnings                   []string            `json:"warnings,omitempty"`
	SkippedNoChange            int                 `json:"skipped_no_change"`
	BuiltWithPrivilege         bool                `json:"built_with_privilege"`
	RequiresExecutionPrivilege bool                `json:"requires_execution_privilege"`
}

type PrivilegeRequiredError struct {
	Path string
	Op   string
	Err  error
}

func (e *PrivilegeRequiredError) Error() string {
	return fmt.Sprintf("%s requires elevated access for %s: %v", e.Path, e.Op, e.Err)
}

func (e *PrivilegeRequiredError) Unwrap() error { return e.Err }

type entryKind string

const (
	entrySymlink       entryKind = "symlink"
	entryRecursiveDir  entryKind = "recursive_dir"
	entryRecursiveFile entryKind = "recursive_file"
	entryCopyDir       entryKind = "copy_dir"
	entryCopyFile      entryKind = "copy_file"
)

type entry struct {
	Profile    config.ResolvedProfile
	Kind       entryKind
	SourcePath string
	TargetPath string
	SourceInfo os.FileInfo
}

type claim struct {
	Profile string
	Path    string
}

type builder struct {
	plan           Plan
	ensureDirs     map[string]int
	currentUID     int
	currentGID     int
	isPrivileged   bool
	previousItems  map[string]state.ManagedItem
	currentEntries map[string]entry
	cleanupExact   map[string]struct{}
}

func Build(resolved config.Resolved, previous *state.File) (Plan, error) {
	entries := make([]entry, 0)
	for _, profile := range resolved.Profiles {
		expanded, err := expandProfile(profile)
		if err != nil {
			return Plan{}, err
		}
		entries = append(entries, expanded...)
	}

	if err := validateClaims(entries); err != nil {
		return Plan{}, err
	}

	currentEntries := make(map[string]entry, len(entries))
	for _, item := range entries {
		currentEntries[item.TargetPath] = item
	}

	previousItems := map[string]state.ManagedItem{}
	if previous != nil {
		previousItems = previous.ItemMap()
	}

	sortEntries(entries)

	b := builder{
		plan: Plan{
			Actions:            make([]Action, 0, len(entries)),
			ObservedItems:      []state.ManagedItem{},
			Warnings:           []string{},
			BuiltWithPrivilege: os.Geteuid() == 0,
		},
		ensureDirs:     map[string]int{},
		currentUID:     os.Geteuid(),
		currentGID:     os.Getegid(),
		isPrivileged:   os.Geteuid() == 0,
		previousItems:  previousItems,
		currentEntries: currentEntries,
		cleanupExact:   map[string]struct{}{},
	}

	if err := b.planCleanup(); err != nil {
		return Plan{}, err
	}

	for _, item := range entries {
		if err := b.planEntry(item); err != nil {
			return Plan{}, err
		}
	}

	for _, action := range b.plan.Actions {
		if action.RequiresPrivilege {
			b.plan.RequiresExecutionPrivilege = true
			break
		}
	}
	if b.plan.BuiltWithPrivilege {
		b.plan.RequiresExecutionPrivilege = true
	}

	return b.plan, nil
}

func expandProfile(profile config.ResolvedProfile) ([]entry, error) {
	info, err := lstat(profile.SourcePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("profile %q source %q contains unsupported symlink", profile.Name, profile.SourcePath)
	}

	entries := make([]entry, 0)
	switch profile.Strategy {
	case config.StrategySymlink:
		entries = append(entries, entry{Profile: profile, Kind: entrySymlink, SourcePath: profile.SourcePath, TargetPath: profile.TargetPath, SourceInfo: info})
	case config.StrategyRecursiveSymlink, config.StrategyCopy:
		if !info.IsDir() {
			if profile.Strategy == config.StrategyRecursiveSymlink {
				return nil, fmt.Errorf("profile %q: recursive_symlink only supports directory sources", profile.Name)
			}
			if profile.ContentsOnly {
				return nil, fmt.Errorf("profile %q: contents_only=true only supports directory sources", profile.Name)
			}
			entries = append(entries, entry{Profile: profile, Kind: entryCopyFile, SourcePath: profile.SourcePath, TargetPath: profile.TargetPath, SourceInfo: info})
			return entries, nil
		}

		if !profile.ContentsOnly {
			if profile.Strategy == config.StrategyRecursiveSymlink {
				entries = append(entries, entry{Profile: profile, Kind: entryRecursiveDir, SourcePath: profile.SourcePath, TargetPath: profile.TargetPath, SourceInfo: info})
			} else {
				entries = append(entries, entry{Profile: profile, Kind: entryCopyDir, SourcePath: profile.SourcePath, TargetPath: profile.TargetPath, SourceInfo: info})
			}
		}

		err = filepath.Walk(profile.SourcePath, func(path string, childInfo os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == profile.SourcePath {
				return nil
			}
			if childInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("profile %q source %q contains unsupported symlink", profile.Name, path)
			}
			rel, err := filepath.Rel(profile.SourcePath, path)
			if err != nil {
				return err
			}
			targetPath := filepath.Join(profile.TargetPath, rel)
			if profile.Strategy == config.StrategyRecursiveSymlink {
				if childInfo.IsDir() {
					entries = append(entries, entry{Profile: profile, Kind: entryRecursiveDir, SourcePath: path, TargetPath: targetPath, SourceInfo: childInfo})
				} else {
					entries = append(entries, entry{Profile: profile, Kind: entryRecursiveFile, SourcePath: path, TargetPath: targetPath, SourceInfo: childInfo})
				}
				return nil
			}
			if childInfo.IsDir() {
				entries = append(entries, entry{Profile: profile, Kind: entryCopyDir, SourcePath: path, TargetPath: targetPath, SourceInfo: childInfo})
			} else {
				entries = append(entries, entry{Profile: profile, Kind: entryCopyFile, SourcePath: path, TargetPath: targetPath, SourceInfo: childInfo})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("profile %q: unsupported strategy %q", profile.Name, profile.Strategy)
	}

	return entries, nil
}

func validateClaims(entries []entry) error {
	claims := make([]claim, 0, len(entries))
	for _, item := range entries {
		current := claim{Profile: item.Profile.Name, Path: filepath.Clean(item.TargetPath)}
		for _, existing := range claims {
			if existing.Profile == current.Profile {
				continue
			}
			if sameOrNested(existing.Path, current.Path) {
				return fmt.Errorf("path conflict between profiles %q and %q at %q", existing.Profile, current.Profile, shorterPath(existing.Path, current.Path))
			}
		}
		claims = append(claims, current)
	}
	return nil
}

func sortEntries(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		leftDepth := pathDepth(entries[i].TargetPath)
		rightDepth := pathDepth(entries[j].TargetPath)
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		leftDir := entries[i].Kind == entryRecursiveDir || entries[i].Kind == entryCopyDir || (entries[i].Kind == entrySymlink && entries[i].SourceInfo.IsDir())
		rightDir := entries[j].Kind == entryRecursiveDir || entries[j].Kind == entryCopyDir || (entries[j].Kind == entrySymlink && entries[j].SourceInfo.IsDir())
		if leftDir != rightDir {
			return leftDir
		}
		return entries[i].TargetPath < entries[j].TargetPath
	})
}

func (b *builder) planEntry(item entry) error {
	allowReplaceParents := item.Profile.Strategy != config.StrategyCopy && item.Profile.SymlinkForce
	if err := b.ensureParentDirs(filepath.Dir(item.TargetPath), item.Profile.Name, allowReplaceParents); err != nil {
		return err
	}

	switch item.Kind {
	case entrySymlink:
		return b.planSymlink(item)
	case entryRecursiveDir:
		return b.planRecursiveDir(item)
	case entryRecursiveFile:
		return b.planRecursiveFile(item)
	case entryCopyDir:
		return b.planCopyDir(item)
	case entryCopyFile:
		return b.planCopyFile(item)
	default:
		return fmt.Errorf("unsupported entry kind %q", item.Kind)
	}
}

func (b *builder) ensureParentDirs(targetDir string, profileName string, allowReplace bool) error {
	if targetDir == "." || targetDir == string(filepath.Separator) || targetDir == "" {
		return nil
	}
	parent := filepath.Dir(targetDir)
	if parent != targetDir {
		if err := b.ensureParentDirs(parent, profileName, allowReplace); err != nil {
			return err
		}
	}
	return b.ensureDir(targetDir, profileName, allowReplace, true, false, false, 0, false, 0, 0, "")
}

func (b *builder) planSymlink(item entry) error {
	if _, ok := b.cleanupExact[item.TargetPath]; ok {
		b.addAction(Action{
			Kind:              ActionSymlink,
			Profile:           item.Profile.Name,
			SourcePath:        item.SourcePath,
			TargetPath:        item.TargetPath,
			RequiresPrivilege: b.needsPrivilege(item.TargetPath, false, 0, 0),
			TrackState:        true,
			CreatedByTool:     true,
			StateKind:         string(state.ItemSymlink),
		})
		return nil
	}

	previous, hasPrevious := b.previousItems[item.TargetPath]
	info, err := lstat(item.TargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			b.addAction(Action{
				Kind:              ActionSymlink,
				Profile:           item.Profile.Name,
				SourcePath:        item.SourcePath,
				TargetPath:        item.TargetPath,
				RequiresPrivilege: b.needsPrivilege(item.TargetPath, false, 0, 0),
				TrackState:        true,
				CreatedByTool:     true,
				StateKind:         string(state.ItemSymlink),
			})
			return nil
		}
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		matchesDesired, err := symlinkMatches(item.TargetPath, item.SourcePath)
		if err != nil {
			if isPermission(err) {
				return &PrivilegeRequiredError{Path: item.TargetPath, Op: "read symlink", Err: err}
			}
			return err
		}
		if matchesDesired {
			if err := b.observeManagedSymlink(item, info, !hasPrevious); err != nil {
				return err
			}
			b.plan.SkippedNoChange++
			return nil
		}
		if hasPrevious && previous.Kind == state.ItemSymlink {
			matchesPrevious, err := symlinkMatches(item.TargetPath, previous.LinkTarget)
			if err != nil {
				return err
			}
			if !matchesPrevious {
				return fmt.Errorf("managed symlink %q was modified after the last successful apply", item.TargetPath)
			}
			b.addAction(Action{
				Kind:               ActionSymlink,
				Profile:            item.Profile.Name,
				SourcePath:         item.SourcePath,
				TargetPath:         item.TargetPath,
				ExistingTarget:     true,
				Replace:            true,
				RequiresPrivilege:  b.needsPrivilege(item.TargetPath, false, 0, 0),
				TrackState:         true,
				CreatedByTool:      true,
				StateKind:          string(state.ItemSymlink),
				ExpectedLinkTarget: previous.LinkTarget,
			})
			return nil
		}
	}

	if !item.Profile.SymlinkForce {
		return fmt.Errorf("target %q already exists and symlink_force is false", item.TargetPath)
	}

	b.addAction(Action{
		Kind:              ActionSymlink,
		Profile:           item.Profile.Name,
		SourcePath:        item.SourcePath,
		TargetPath:        item.TargetPath,
		ExistingTarget:    true,
		Replace:           true,
		ReplaceRecursive:  info.IsDir(),
		RequiresPrivilege: b.needsPrivilege(item.TargetPath, false, 0, 0),
		TrackState:        true,
		CreatedByTool:     true,
		StateKind:         string(state.ItemSymlink),
	})
	return nil
}

func (b *builder) planRecursiveDir(item entry) error {
	return b.ensureDir(item.TargetPath, item.Profile.Name, item.Profile.SymlinkForce, false, false, false, 0, false, 0, 0, "")
}

func (b *builder) planRecursiveFile(item entry) error {
	return b.planSymlink(item)
}

func (b *builder) planCopyDir(item entry) error {
	previous, hasPrevious := b.previousItems[item.TargetPath]
	desiredUID, desiredGID, ownerLabel, err := desiredOwnership(item.SourceInfo, item.Profile.Permissions)
	if err != nil {
		return fmt.Errorf("profile %q owner: %w", item.Profile.Name, err)
	}
	desiredMode := item.SourceInfo.Mode().Perm()
	if item.Profile.Permissions.DirMode != "" {
		mode, err := parseMode(item.Profile.Permissions.DirMode)
		if err != nil {
			return err
		}
		desiredMode = mode
	}
	if _, ok := b.cleanupExact[item.TargetPath]; ok {
		return b.ensureDir(item.TargetPath, item.Profile.Name, false, false, true, true, desiredMode, true, desiredUID, desiredGID, ownerLabel)
	}

	targetInfo, err := lstat(item.TargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return b.ensureDir(item.TargetPath, item.Profile.Name, false, false, true, true, desiredMode, true, desiredUID, desiredGID, ownerLabel)
		}
		return err
	}
	if !targetInfo.IsDir() {
		return fmt.Errorf("copy target %q conflicts with an existing non-directory", item.TargetPath)
	}
	if !hasPrevious || previous.Kind != state.ItemDir {
		return fmt.Errorf("copy target directory %q already exists and is not a previously managed copy directory; please inspect and remove it manually", item.TargetPath)
	}

	currentUID, currentGID, err := statOwnership(targetInfo)
	if err != nil {
		return err
	}
	modeEqual := targetInfo.Mode().Perm() == desiredMode
	ownerEqual := currentUID == desiredUID && currentGID == desiredGID
	if modeEqual && ownerEqual {
		b.plan.SkippedNoChange++
		return nil
	}

	return b.ensureDir(item.TargetPath, item.Profile.Name, false, false, true, true, desiredMode, true, desiredUID, desiredGID, ownerLabel)
}

func (b *builder) planCopyFile(item entry) error {
	if _, ok := b.cleanupExact[item.TargetPath]; ok {
		sourceBytes, err := os.ReadFile(item.SourcePath)
		if err != nil {
			return err
		}
		desiredUID, desiredGID, ownerLabel, err := desiredOwnership(item.SourceInfo, item.Profile.Permissions)
		if err != nil {
			return fmt.Errorf("profile %q owner: %w", item.Profile.Name, err)
		}
		desiredMode := item.SourceInfo.Mode().Perm()
		if item.Profile.Permissions.FileMode != "" {
			mode, err := parseMode(item.Profile.Permissions.FileMode)
			if err != nil {
				return err
			}
			desiredMode = mode
		}
		return b.addCopyAction(item, false, false, sourceBytes, nil, desiredUID, desiredGID, ownerLabel, desiredMode, "")
	}
	previous, hasPrevious := b.previousItems[item.TargetPath]
	desiredUID, desiredGID, ownerLabel, err := desiredOwnership(item.SourceInfo, item.Profile.Permissions)
	if err != nil {
		return fmt.Errorf("profile %q owner: %w", item.Profile.Name, err)
	}
	desiredMode := item.SourceInfo.Mode().Perm()
	if item.Profile.Permissions.FileMode != "" {
		mode, err := parseMode(item.Profile.Permissions.FileMode)
		if err != nil {
			return err
		}
		desiredMode = mode
	}

	sourceBytes, err := os.ReadFile(item.SourcePath)
	if err != nil {
		return err
	}

	targetInfo, err := lstat(item.TargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return b.addCopyAction(item, false, false, sourceBytes, nil, desiredUID, desiredGID, ownerLabel, desiredMode, "")
		}
		return err
	}
	if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("copy target %q conflicts with an existing non-file", item.TargetPath)
	}
	if !hasPrevious || previous.Kind != state.ItemFile {
		return fmt.Errorf("copy target %q already exists and is not a previously managed copy file", item.TargetPath)
	}

	targetBytes, err := os.ReadFile(item.TargetPath)
	if err != nil {
		if isPermission(err) {
			return &PrivilegeRequiredError{Path: item.TargetPath, Op: "read file", Err: err}
		}
		return err
	}

	currentUID, currentGID, err := statOwnership(targetInfo)
	if err != nil {
		return err
	}
	targetHash := hashBytes(targetBytes)
	if targetHash != previous.ContentHash || currentUID != previous.UID || currentGID != previous.GID || uint32(targetInfo.Mode().Perm()) != previous.Mode.MustUint32() {
		return fmt.Errorf("managed copy file %q was modified after the last successful apply", item.TargetPath)
	}
	contentEqual := bytes.Equal(sourceBytes, targetBytes)
	ownerEqual := currentUID == desiredUID && currentGID == desiredGID
	modeEqual := targetInfo.Mode().Perm() == desiredMode
	if contentEqual && ownerEqual && modeEqual {
		b.plan.SkippedNoChange++
		return nil
	}

	return b.addCopyAction(item, true, contentEqual, sourceBytes, targetBytes, desiredUID, desiredGID, ownerLabel, desiredMode, previous.ContentHash)
}

func (b *builder) addCopyAction(item entry, existingTarget bool, metadataOnly bool, sourceBytes []byte, targetBytes []byte, desiredUID int, desiredGID int, ownerLabel string, desiredMode os.FileMode, expectedHash string) error {
	diffText := ""
	diffSummary := ""
	if existingTarget && !metadataOnly {
		diffText, diffSummary = buildDiffPreview(item.TargetPath, sourceBytes, targetBytes)
	}
	b.addAction(Action{
		Kind:               ActionCopyFile,
		Profile:            item.Profile.Name,
		SourcePath:         item.SourcePath,
		TargetPath:         item.TargetPath,
		ExistingTarget:     existingTarget,
		ContentChanged:     !metadataOnly,
		MetadataOnly:       metadataOnly,
		RequiresPrivilege:  b.needsPrivilege(item.TargetPath, true, desiredUID, desiredGID),
		ManageOwner:        true,
		DesiredUID:         desiredUID,
		DesiredGID:         desiredGID,
		OwnerLabel:         ownerLabel,
		ManageMode:         true,
		DesiredMode:        uint32(desiredMode.Perm()),
		DesiredModeOctal:   fmt.Sprintf("%04o", desiredMode.Perm()),
		Diff:               diffText,
		DiffSummary:        diffSummary,
		ManagedPathIsDir:   false,
		SourceOwnerLabel:   ownerLabel,
		SourceModeOctal:    fmt.Sprintf("%04o", item.SourceInfo.Mode().Perm()),
		DesiredTargetLabel: item.TargetPath,
		TrackState:         true,
		CreatedByTool:      true,
		StateKind:          string(state.ItemFile),
		ExpectedHash:       expectedHash,
		SourceHash:         hashBytes(sourceBytes),
	})
	return nil
}

func (b *builder) observeManagedSymlink(item entry, info os.FileInfo, adopted bool) error {
	uid, gid, err := statOwnership(info)
	if err != nil {
		return err
	}
	b.plan.ObservedItems = append(b.plan.ObservedItems, state.ManagedItem{
		Path:          item.TargetPath,
		Profile:       item.Profile.Name,
		Kind:          state.ItemSymlink,
		Strategy:      string(item.Profile.Strategy),
		LinkTarget:    item.SourcePath,
		UID:           uid,
		GID:           gid,
		Mode:          state.ModeFromUint32(uint32(info.Mode().Perm())),
		CreatedByTool: !adopted,
	})
	return nil
}

func (b *builder) ensureDir(targetPath string, profileName string, allowReplace bool, autoParent bool, manageOwner bool, manageMode bool, desiredMode os.FileMode, ownerManaged bool, desiredUID int, desiredGID int, ownerLabel string) error {
	if idx, ok := b.ensureDirs[targetPath]; ok {
		action := b.plan.Actions[idx]
		action.Replace = action.Replace || allowReplace
		action.ReplaceRecursive = action.ReplaceRecursive || allowReplace
		action.ManageOwner = action.ManageOwner || ownerManaged
		action.ManageMode = action.ManageMode || manageMode
		if ownerManaged {
			action.DesiredUID = desiredUID
			action.DesiredGID = desiredGID
			action.OwnerLabel = ownerLabel
		}
		if manageMode {
			action.DesiredMode = uint32(desiredMode.Perm())
			action.DesiredModeOctal = fmt.Sprintf("%04o", desiredMode.Perm())
		}
		action.RequiresPrivilege = action.RequiresPrivilege || b.needsPrivilege(targetPath, ownerManaged, desiredUID, desiredGID)
		action.TrackState = true
		action.StateKind = string(state.ItemDir)
		b.plan.Actions[idx] = action
		return nil
	}

	info, err := statDirCandidate(targetPath, autoParent)
	if err != nil {
		if os.IsNotExist(err) {
			action := Action{
				Kind:              ActionEnsureDir,
				Profile:           profileName,
				TargetPath:        targetPath,
				AutoParent:        autoParent,
				RequiresPrivilege: b.needsPrivilege(targetPath, ownerManaged, desiredUID, desiredGID),
				ManageOwner:       ownerManaged,
				DesiredUID:        desiredUID,
				DesiredGID:        desiredGID,
				OwnerLabel:        ownerLabel,
				ManageMode:        manageMode,
				DesiredMode:       uint32(desiredMode.Perm()),
				DesiredModeOctal:  fmt.Sprintf("%04o", desiredMode.Perm()),
				ManagedPathIsDir:  true,
				TrackState:        true,
				StateKind:         string(state.ItemDir),
				CreatedByTool:     true,
			}
			b.addEnsureDir(action)
			return nil
		}
		return err
	}

	if !info.IsDir() {
		if !allowReplace {
			return fmt.Errorf("target %q must be a directory", targetPath)
		}
		action := Action{
			Kind:              ActionEnsureDir,
			Profile:           profileName,
			TargetPath:        targetPath,
			AutoParent:        autoParent,
			ExistingTarget:    true,
			Replace:           true,
			ReplaceRecursive:  true,
			RequiresPrivilege: b.needsPrivilege(targetPath, ownerManaged, desiredUID, desiredGID),
			ManageOwner:       ownerManaged,
			DesiredUID:        desiredUID,
			DesiredGID:        desiredGID,
			OwnerLabel:        ownerLabel,
			ManageMode:        manageMode,
			DesiredMode:       uint32(desiredMode.Perm()),
			DesiredModeOctal:  fmt.Sprintf("%04o", desiredMode.Perm()),
			ManagedPathIsDir:  true,
			TrackState:        true,
			StateKind:         string(state.ItemDir),
			CreatedByTool:     true,
		}
		b.addEnsureDir(action)
		return nil
	}

	if !manageMode && !ownerManaged {
		if !autoParent {
			b.plan.SkippedNoChange++
		}
		return nil
	}
	currentUID, currentGID, err := statOwnership(info)
	if err != nil {
		return err
	}
	modeEqual := !manageMode || info.Mode().Perm() == desiredMode
	ownerEqual := !ownerManaged || (currentUID == desiredUID && currentGID == desiredGID)
	if modeEqual && ownerEqual {
		b.plan.SkippedNoChange++
		return nil
	}

	action := Action{
		Kind:              ActionEnsureDir,
		Profile:           profileName,
		TargetPath:        targetPath,
		AutoParent:        autoParent,
		ExistingTarget:    true,
		MetadataOnly:      true,
		RequiresPrivilege: b.needsPrivilege(targetPath, ownerManaged, desiredUID, desiredGID),
		ManageOwner:       ownerManaged,
		DesiredUID:        desiredUID,
		DesiredGID:        desiredGID,
		OwnerLabel:        ownerLabel,
		ManageMode:        manageMode,
		DesiredMode:       uint32(desiredMode.Perm()),
		DesiredModeOctal:  fmt.Sprintf("%04o", desiredMode.Perm()),
		ManagedPathIsDir:  true,
		TrackState:        true,
		StateKind:         string(state.ItemDir),
		CreatedByTool:     false,
	}
	b.addEnsureDir(action)
	return nil
}

func (b *builder) addEnsureDir(action Action) {
	b.ensureDirs[action.TargetPath] = len(b.plan.Actions)
	b.plan.Actions = append(b.plan.Actions, action)
}

func (b *builder) addAction(action Action) {
	b.plan.Actions = append(b.plan.Actions, action)
}

func (b *builder) needsPrivilege(targetPath string, ownerManaged bool, desiredUID int, desiredGID int) bool {
	if b.isPrivileged {
		return false
	}
	if ownerManaged && (desiredUID != b.currentUID || desiredGID != b.currentGID) {
		return true
	}
	checkPath := targetPath
	for {
		info, err := os.Lstat(checkPath)
		if err == nil {
			if !info.IsDir() {
				checkPath = filepath.Dir(checkPath)
			}
			break
		}
		if !os.IsNotExist(err) {
			return true
		}
		next := filepath.Dir(checkPath)
		if next == checkPath {
			break
		}
		checkPath = next
	}
	return unix.Access(checkPath, unix.W_OK|unix.X_OK) != nil
}

func buildDiffPreview(targetPath string, sourceBytes []byte, targetBytes []byte) (string, string) {
	if isBinary(sourceBytes) || isBinary(targetBytes) {
		return "", fmt.Sprintf("binary content change (%dB -> %dB)", len(targetBytes), len(sourceBytes))
	}
	if len(sourceBytes) > diffMaxBytes || len(targetBytes) > diffMaxBytes {
		return "", fmt.Sprintf("large text change (%dB -> %dB)", len(targetBytes), len(sourceBytes))
	}

	sourceLines := splitLines(string(sourceBytes))
	targetLines := splitLines(string(targetBytes))
	if len(sourceLines)+len(targetLines) > diffMaxLines {
		return "", fmt.Sprintf("large text diff (%d lines -> %d lines)", len(targetLines), len(sourceLines))
	}

	ud := difflib.UnifiedDiff{
		A:        targetLines,
		B:        sourceLines,
		FromFile: targetPath + " (current)",
		ToFile:   targetPath + " (source)",
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return "", "unable to generate diff"
	}
	if strings.TrimSpace(text) == "" {
		return "", "text content changed"
	}
	return text, "text diff"
}

func Format(plan Plan) string {
	if len(plan.Actions) == 0 && len(plan.Warnings) == 0 {
		return "No changes required.\n"
	}

	var lines []string
	if len(plan.Actions) > 0 {
		lines = append(lines, "Planned changes:")
	}
	for _, action := range plan.Actions {
		lines = append(lines, "  - "+describeAction(action))
		if action.Diff != "" {
			for _, line := range strings.Split(strings.TrimSuffix(action.Diff, "\n"), "\n") {
				lines = append(lines, "      "+line)
			}
		} else if action.DiffSummary != "" {
			lines = append(lines, "      "+action.DiffSummary)
		}
	}
	if len(plan.Warnings) > 0 {
		lines = append(lines, "Warnings:")
		for _, warning := range plan.Warnings {
			lines = append(lines, "  - "+warning)
		}
	}
	lines = append(lines, fmt.Sprintf("Summary: %d change(s), %d skipped as unchanged", len(plan.Actions), plan.SkippedNoChange))
	if plan.RequiresExecutionPrivilege {
		lines = append(lines, "Execution requires elevated privileges.")
	}
	return strings.Join(lines, "\n") + "\n"
}

func describeAction(action Action) string {
	switch action.Kind {
	case ActionEnsureDir:
		if action.Replace {
			return fmt.Sprintf("replace with directory %s", action.TargetPath)
		}
		if action.MetadataOnly {
			parts := []string{fmt.Sprintf("update directory metadata %s", action.TargetPath)}
			if action.ManageOwner {
				parts = append(parts, fmt.Sprintf("owner=%s", action.OwnerLabel))
			}
			if action.ManageMode {
				parts = append(parts, fmt.Sprintf("mode=%s", action.DesiredModeOctal))
			}
			return strings.Join(parts, " ")
		}
		if action.AutoParent {
			return fmt.Sprintf("create parent directory %s", action.TargetPath)
		}
		return fmt.Sprintf("create directory %s", action.TargetPath)
	case ActionSymlink:
		if action.Replace {
			return fmt.Sprintf("replace with symlink %s -> %s", action.TargetPath, action.SourcePath)
		}
		return fmt.Sprintf("create symlink %s -> %s", action.TargetPath, action.SourcePath)
	case ActionCopyFile:
		if action.MetadataOnly {
			parts := []string{fmt.Sprintf("update file metadata %s", action.TargetPath)}
			if action.ManageOwner {
				parts = append(parts, fmt.Sprintf("owner=%s", action.OwnerLabel))
			}
			if action.ManageMode {
				parts = append(parts, fmt.Sprintf("mode=%s", action.DesiredModeOctal))
			}
			return strings.Join(parts, " ")
		}
		if action.ExistingTarget {
			return fmt.Sprintf("overwrite file %s <- %s", action.TargetPath, action.SourcePath)
		}
		return fmt.Sprintf("copy file %s <- %s", action.TargetPath, action.SourcePath)
	case ActionRemoveFile:
		return fmt.Sprintf("remove managed file %s", action.TargetPath)
	case ActionRemoveSymlink:
		return fmt.Sprintf("remove managed symlink %s", action.TargetPath)
	case ActionRemoveDirIfEmpty:
		return fmt.Sprintf("remove managed directory if empty %s", action.TargetPath)
	default:
		return fmt.Sprintf("%s %s", action.Kind, action.TargetPath)
	}
}

func lstat(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil && isPermission(err) {
		return nil, &PrivilegeRequiredError{Path: path, Op: "stat", Err: err}
	}
	return info, err
}

func statDirCandidate(path string, followSymlink bool) (os.FileInfo, error) {
	if !followSymlink {
		return lstat(path)
	}
	info, err := os.Stat(path)
	if err == nil {
		return info, nil
	}
	if isPermission(err) {
		return nil, &PrivilegeRequiredError{Path: path, Op: "stat", Err: err}
	}
	if os.IsNotExist(err) {
		linkInfo, linkErr := os.Lstat(path)
		if linkErr == nil {
			return linkInfo, nil
		}
		if isPermission(linkErr) {
			return nil, &PrivilegeRequiredError{Path: path, Op: "stat", Err: linkErr}
		}
	}
	return nil, err
}

func isPermission(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func parseMode(raw string) (os.FileMode, error) {
	value, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: %w", raw, err)
	}
	return os.FileMode(value), nil
}

func desiredOwnership(info os.FileInfo, permissions config.Permissions) (int, int, string, error) {
	if permissions.Owner != "" {
		uid, gid, label, err := lookupOwner(permissions.Owner)
		return uid, gid, label, err
	}
	uid, gid, err := statOwnership(info)
	if err != nil {
		return 0, 0, "", err
	}
	return uid, gid, fmt.Sprintf("%d:%d", uid, gid), nil
}

func lookupOwner(spec string) (int, int, string, error) {
	parts := strings.SplitN(spec, ":", 2)
	usr, err := user.Lookup(parts[0])
	if err != nil {
		return 0, 0, "", err
	}
	uid, err := strconv.Atoi(usr.Uid)
	if err != nil {
		return 0, 0, "", err
	}
	gid := usr.Gid
	label := parts[0]
	if len(parts) == 2 {
		grp, err := user.LookupGroup(parts[1])
		if err != nil {
			return 0, 0, "", err
		}
		gid = grp.Gid
		label = spec
	}
	parsedGID, err := strconv.Atoi(gid)
	if err != nil {
		return 0, 0, "", err
	}
	return uid, parsedGID, label, nil
}

func statOwnership(info os.FileInfo) (int, int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("unsupported file info type for %s", info.Name())
	}
	return int(stat.Uid), int(stat.Gid), nil
}

func splitLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.SplitAfter(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		return append(lines[:len(lines)-1], lines[len(lines)-1]+"\n")
	}
	return lines
}

func isBinary(content []byte) bool {
	if bytes.IndexByte(content, 0) >= 0 {
		return true
	}
	return !utf8.Valid(content)
}

func sameOrNested(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	leftRel, err := filepath.Rel(left, right)
	if err == nil && leftRel != "." && leftRel != ".." && !strings.HasPrefix(leftRel, ".."+string(filepath.Separator)) {
		return true
	}
	rightRel, err := filepath.Rel(right, left)
	if err == nil && rightRel != "." && rightRel != ".." && !strings.HasPrefix(rightRel, ".."+string(filepath.Separator)) {
		return true
	}
	return false
}

func shorterPath(left string, right string) string {
	if pathDepth(left) <= pathDepth(right) {
		return left
	}
	return right
}

func pathDepth(path string) int {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return 1
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	return len(parts)
}
