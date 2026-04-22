package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dotfile-manager/internal/state"
)

func (b *builder) planCleanup() error {
	items := make([]state.ManagedItem, 0, len(b.previousItems))
	for _, item := range b.previousItems {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		leftDepth := pathDepth(items[i].Path)
		rightDepth := pathDepth(items[j].Path)
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind != state.ItemDir
		}
		return items[i].Path > items[j].Path
	})

	for _, item := range items {
		if current, ok := b.currentEntries[item.Path]; ok {
			if stateKindForEntry(current) == item.Kind {
				continue
			}
			if err := b.planCleanupItem(item, true); err != nil {
				return err
			}
			b.cleanupExact[item.Path] = struct{}{}
			continue
		}
		if item.Kind == state.ItemDir && b.hasCurrentDescendant(item.Path) {
			continue
		}
		if err := b.planCleanupItem(item, false); err != nil {
			return err
		}
		b.cleanupExact[item.Path] = struct{}{}
	}
	return nil
}

func (b *builder) planCleanupItem(item state.ManagedItem, exactReuse bool) error {
	info, err := lstat(item.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	switch item.Kind {
	case state.ItemFile:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed file %q no longer matches recorded type", item.Path)
		}
		hashValue, err := hashFile(item.Path)
		if err != nil {
			return err
		}
		uid, gid, err := statOwnership(info)
		if err != nil {
			return err
		}
		if hashValue != item.ContentHash || uid != item.UID || gid != item.GID || uint32(info.Mode().Perm()) != item.Mode.MustUint32() {
			return fmt.Errorf("managed copy file %q was modified after the last successful apply", item.Path)
		}
		b.addAction(Action{
			Kind:              ActionRemoveFile,
			Profile:           item.Profile,
			TargetPath:        item.Path,
			StateKind:         string(item.Kind),
			RequiresPrivilege: b.needsPrivilege(item.Path, false, 0, 0),
			ExpectedHash:      item.ContentHash,
		})
	case state.ItemSymlink:
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("managed symlink %q no longer matches recorded type", item.Path)
		}
		linkTarget, err := os.Readlink(item.Path)
		if err != nil {
			return err
		}
		if linkTarget != item.LinkTarget {
			return fmt.Errorf("managed symlink %q was modified after the last successful apply", item.Path)
		}
		b.addAction(Action{
			Kind:               ActionRemoveSymlink,
			Profile:            item.Profile,
			TargetPath:         item.Path,
			StateKind:          string(item.Kind),
			RequiresPrivilege:  b.needsPrivilege(item.Path, false, 0, 0),
			ExpectedLinkTarget: item.LinkTarget,
		})
	case state.ItemDir:
		if !info.IsDir() {
			return fmt.Errorf("managed directory %q no longer matches recorded type", item.Path)
		}
		unmanaged, err := b.findUnmanagedChildren(item.Path)
		if err != nil {
			return err
		}
		if len(unmanaged) > 0 {
			b.plan.Warnings = append(b.plan.Warnings, fmt.Sprintf("directory %s contains unmanaged entries and will be kept: %s", item.Path, strings.Join(unmanaged, ", ")))
			if exactReuse {
				return fmt.Errorf("directory %q still contains unmanaged entries and cannot be reused automatically", item.Path)
			}
		}
		b.addAction(Action{
			Kind:              ActionRemoveDirIfEmpty,
			Profile:           item.Profile,
			TargetPath:        item.Path,
			StateKind:         string(item.Kind),
			RequiresPrivilege: b.needsPrivilege(item.Path, false, 0, 0),
		})
	default:
		return fmt.Errorf("unsupported managed item kind %q", item.Kind)
	}

	return nil
}

func (b *builder) hasCurrentDescendant(path string) bool {
	for currentPath := range b.currentEntries {
		if currentPath == path {
			continue
		}
		rel, err := filepath.Rel(path, currentPath)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (b *builder) findUnmanagedChildren(path string) ([]string, error) {
	managed := map[string]struct{}{}
	for itemPath := range b.previousItems {
		if itemPath == path {
			continue
		}
		rel, err := filepath.Rel(path, itemPath)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			managed[itemPath] = struct{}{}
		}
	}

	unmanaged := []string{}
	err := filepath.Walk(path, func(childPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if childPath == path {
			return nil
		}
		if _, ok := managed[childPath]; ok {
			return nil
		}
		unmanaged = append(unmanaged, childPath)
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(unmanaged) > 3 {
		return unmanaged[:3], nil
	}
	return unmanaged, nil
}

func stateKindForEntry(item entry) state.ItemKind {
	switch item.Kind {
	case entryCopyFile:
		return state.ItemFile
	case entryCopyDir, entryRecursiveDir:
		return state.ItemDir
	case entrySymlink, entryRecursiveFile:
		return state.ItemSymlink
	default:
		return state.ItemFile
	}
}

func hashFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(content), nil
}

func hashBytes(content []byte) string {
	hashValue := sha256.Sum256(content)
	return hex.EncodeToString(hashValue[:])
}

func symlinkMatches(linkPath string, expectedTarget string) (bool, error) {
	rawTarget, err := os.Readlink(linkPath)
	if err != nil {
		return false, err
	}
	if filepath.Clean(rawTarget) == filepath.Clean(expectedTarget) {
		return true, nil
	}
	resolvedLink, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return false, err
	}
	resolvedExpected, err := filepath.EvalSymlinks(expectedTarget)
	if err != nil {
		resolvedExpected, err = filepath.Abs(expectedTarget)
		if err != nil {
			return false, err
		}
	}
	return filepath.Clean(resolvedLink) == filepath.Clean(resolvedExpected), nil
}
