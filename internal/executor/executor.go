package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"dotfile-manager/internal/planner"
)

func Apply(plan planner.Plan, stdout io.Writer) error {
	for _, action := range plan.Actions {
		if _, err := fmt.Fprintf(stdout, "Applying: %s\n", describeAction(action)); err != nil {
			return err
		}
		if err := applyAction(action); err != nil {
			return err
		}
	}
	return nil
}

func describeAction(action planner.Action) string {
	switch action.Kind {
	case planner.ActionEnsureDir:
		return action.TargetPath
	case planner.ActionSymlink:
		return fmt.Sprintf("%s -> %s", action.TargetPath, action.SourcePath)
	case planner.ActionCopyFile:
		return action.TargetPath
	case planner.ActionRemoveFile, planner.ActionRemoveSymlink, planner.ActionRemoveDirIfEmpty:
		return action.TargetPath
	default:
		return action.TargetPath
	}
}

func applyAction(action planner.Action) error {
	switch action.Kind {
	case planner.ActionEnsureDir:
		return applyEnsureDir(action)
	case planner.ActionSymlink:
		return applySymlink(action)
	case planner.ActionCopyFile:
		return applyCopyFile(action)
	case planner.ActionRemoveFile:
		return applyRemoveFile(action)
	case planner.ActionRemoveSymlink:
		return applyRemoveSymlink(action)
	case planner.ActionRemoveDirIfEmpty:
		return removeDirIfEmpty(action.TargetPath)
	default:
		return fmt.Errorf("unsupported action kind %q", action.Kind)
	}
}

func applyEnsureDir(action planner.Action) error {
	if action.Replace {
		if err := removeExisting(action.TargetPath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(action.TargetPath, 0o755); err != nil {
		return err
	}
	return applyMetadata(action.TargetPath, action.ManageOwner, action.DesiredUID, action.DesiredGID, action.ManageMode, os.FileMode(action.DesiredMode))
}

func applySymlink(action planner.Action) error {
	if action.Replace {
		if err := removeExisting(action.TargetPath); err != nil {
			return err
		}
	}
	if err := os.Symlink(action.SourcePath, action.TargetPath); err != nil {
		return err
	}
	return nil
}

func applyCopyFile(action planner.Action) error {
	sourceContent, err := os.ReadFile(action.SourcePath)
	if err != nil {
		return err
	}
	if action.SourceHash != "" {
		hashValue := hashBytes(sourceContent)
		if hashValue != action.SourceHash {
			return fmt.Errorf("source %q changed after preview; aborting copy", action.SourcePath)
		}
	}
	if action.ExistingTarget && action.ExpectedHash != "" {
		hashValue, err := hashFile(action.TargetPath)
		if err != nil {
			return err
		}
		if hashValue != action.ExpectedHash {
			return fmt.Errorf("target %q changed after preview; aborting copy", action.TargetPath)
		}
	}
	if action.ContentChanged {
		if err := copyFileContent(sourceContent, action.TargetPath, os.FileMode(action.DesiredMode)); err != nil {
			return err
		}
	}
	return applyMetadata(action.TargetPath, action.ManageOwner, action.DesiredUID, action.DesiredGID, action.ManageMode, os.FileMode(action.DesiredMode))
}

func applyRemoveFile(action planner.Action) error {
	if action.ExpectedHash != "" {
		hashValue, err := hashFile(action.TargetPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if hashValue != action.ExpectedHash {
			return fmt.Errorf("managed file %q changed after preview; aborting cleanup", action.TargetPath)
		}
	}
	return removeExisting(action.TargetPath)
}

func applyRemoveSymlink(action planner.Action) error {
	if action.ExpectedLinkTarget != "" {
		matches, err := plannerMatchesSymlink(action.TargetPath, action.ExpectedLinkTarget)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !matches {
			return fmt.Errorf("managed symlink %q changed after preview; aborting cleanup", action.TargetPath)
		}
	}
	return removeExisting(action.TargetPath)
}

func copyFileContent(content []byte, targetPath string, mode os.FileMode) error {
	parent := filepath.Dir(targetPath)
	tempFile, err := os.CreateTemp(parent, ".dotfile-manager-*")
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
	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func applyMetadata(targetPath string, manageOwner bool, uid int, gid int, manageMode bool, mode os.FileMode) error {
	if manageOwner {
		currentUID, currentGID, err := fileOwnership(targetPath)
		if err != nil {
			return err
		}
		if currentUID != uid || currentGID != gid {
			if err := os.Chown(targetPath, uid, gid); err != nil {
				return err
			}
		}
	}
	if manageMode {
		info, err := os.Lstat(targetPath)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != mode.Perm() {
			if err := os.Chmod(targetPath, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeExisting(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func removeDirIfEmpty(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	if os.IsExist(err) {
		return nil
	}
	if pe, ok := err.(*os.PathError); ok && pe.Err == syscall.ENOTEMPTY {
		return nil
	}
	return err
}

func hashBytes(content []byte) string {
	hashValue := sha256.Sum256(content)
	return hex.EncodeToString(hashValue[:])
}

func plannerMatchesSymlink(linkPath string, expectedTarget string) (bool, error) {
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

func hashFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hashValue := sha256.Sum256(content)
	return hex.EncodeToString(hashValue[:]), nil
}

func fileOwnership(path string) (int, int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("unsupported file info for %s", path)
	}
	return int(stat.Uid), int(stat.Gid), nil
}

func LookupOwner(spec string) (int, int, error) {
	parts := strings.SplitN(spec, ":", 2)
	usr, err := user.Lookup(parts[0])
	if err != nil {
		return 0, 0, err
	}
	uid, err := strconv.Atoi(usr.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid := usr.Gid
	if len(parts) == 2 {
		grp, err := user.LookupGroup(parts[1])
		if err != nil {
			return 0, 0, err
		}
		gid = grp.Gid
	}
	parsedGID, err := strconv.Atoi(gid)
	if err != nil {
		return 0, 0, err
	}
	return uid, parsedGID, nil
}
