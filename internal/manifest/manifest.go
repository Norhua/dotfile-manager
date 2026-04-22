package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"dotfile-manager/internal/config"
	"dotfile-manager/internal/planner"
	"dotfile-manager/internal/state"
)

func Build(resolved config.Resolved, plan planner.Plan, previous *state.File) (state.File, error) {
	items := map[string]state.ManagedItem{}
	desiredKeepPaths, err := desiredManagedAndParentPaths(resolved)
	if err != nil {
		return state.File{}, err
	}
	if previous != nil {
		for _, item := range previous.Items {
			if _, ok := desiredKeepPaths[item.Path]; ok {
				items[item.Path] = item
			}
		}
	}

	for _, action := range plan.Actions {
		switch action.Kind {
		case planner.ActionRemoveFile, planner.ActionRemoveSymlink, planner.ActionRemoveDirIfEmpty:
			delete(items, action.TargetPath)
		default:
			if !action.TrackState {
				continue
			}
			item, keep, err := deriveManagedItem(action, items[action.TargetPath])
			if err != nil {
				return state.File{}, err
			}
			if keep {
				items[action.TargetPath] = item
			}
		}
	}
	for _, item := range plan.ObservedItems {
		items[item.Path] = item
	}

	result := state.File{
		Version:    state.Version,
		ConfigPath: resolved.ConfigPath,
		Host:       resolved.Host,
		Items:      make([]state.ManagedItem, 0, len(items)),
	}
	for _, item := range items {
		result.Items = append(result.Items, item)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		return result.Items[i].Path < result.Items[j].Path
	})
	return result, nil
}

func desiredManagedAndParentPaths(resolved config.Resolved) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	for _, profile := range resolved.Profiles {
		if err := collectProfilePaths(profile, result); err != nil {
			return nil, err
		}
	}
	for path := range cloneKeys(result) {
		addParentPaths(path, result)
	}
	return result, nil
}

func collectProfilePaths(profile config.ResolvedProfile, result map[string]struct{}) error {
	info, err := os.Lstat(profile.SourcePath)
	if err != nil {
		return err
	}
	if profile.Strategy == config.StrategySymlink {
		result[profile.TargetPath] = struct{}{}
		return nil
	}
	if !info.IsDir() {
		result[profile.TargetPath] = struct{}{}
		return nil
	}
	if !profile.ContentsOnly {
		result[profile.TargetPath] = struct{}{}
	}
	return filepath.Walk(profile.SourcePath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == profile.SourcePath {
			return nil
		}
		rel, err := filepath.Rel(profile.SourcePath, path)
		if err != nil {
			return err
		}
		result[filepath.Join(profile.TargetPath, rel)] = struct{}{}
		return nil
	})
}

func addParentPaths(path string, result map[string]struct{}) {
	current := filepath.Dir(path)
	for current != "." && current != string(filepath.Separator) && current != "" {
		result[current] = struct{}{}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
}

func cloneKeys(source map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(source))
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}

func deriveManagedItem(action planner.Action, previous state.ManagedItem) (state.ManagedItem, bool, error) {
	item := state.ManagedItem{
		Path:          action.TargetPath,
		Profile:       action.Profile,
		Kind:          state.ItemKind(action.StateKind),
		CreatedByTool: action.CreatedByTool || previous.CreatedByTool,
	}

	switch item.Kind {
	case state.ItemSymlink:
		item.Strategy = string(config.StrategySymlink)
		item.LinkTarget = action.SourcePath
		item.UID = previous.UID
		item.GID = previous.GID
		item.Mode = previous.Mode
		return item, true, nil
	case state.ItemFile:
		item.Strategy = string(config.StrategyCopy)
		item.ContentHash = action.SourceHash
		item.UID = action.DesiredUID
		item.GID = action.DesiredGID
		item.Mode = state.ModeFromUint32(action.DesiredMode)
		return item, true, nil
	case state.ItemDir:
		if !item.CreatedByTool && previous.Path == "" {
			return state.ManagedItem{}, false, nil
		}
		item.Strategy = previous.Strategy
		if item.Strategy == "" {
			item.Strategy = string(config.StrategyCopy)
		}
		item.UID = previous.UID
		item.GID = previous.GID
		item.Mode = previous.Mode
		if action.ManageOwner {
			item.UID = action.DesiredUID
			item.GID = action.DesiredGID
		}
		if action.ManageMode {
			item.Mode = state.ModeFromUint32(action.DesiredMode)
		}
		return item, true, nil
	default:
		return state.ManagedItem{}, false, fmt.Errorf("unsupported action state kind %q", action.StateKind)
	}
}
