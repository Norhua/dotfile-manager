package privilege

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"dotfile-manager/internal/config"
	"dotfile-manager/internal/planner"
)

const (
	ModePlan  = "plan"
	ModeApply = "apply"
)

func IsRoot() bool {
	return os.Geteuid() == 0
}

func RunPlanHelper(resolved config.Resolved, stdout io.Writer, stderr io.Writer) (planner.Plan, error) {
	inputPath, cleanupInput, err := writeTempJSON("resolved-*.json", resolved)
	if err != nil {
		return planner.Plan{}, err
	}
	defer cleanupInput()

	outputFile, err := os.CreateTemp("", "plan-*.json")
	if err != nil {
		return planner.Plan{}, err
	}
	outputPath := outputFile.Name()
	_ = outputFile.Close()
	defer os.Remove(outputPath)

	if err := runSudoHelper([]string{"--internal-mode", ModePlan, "--internal-input", inputPath, "--internal-output", outputPath}, stdout, stderr); err != nil {
		return planner.Plan{}, err
	}

	var plan planner.Plan
	if err := readJSON(outputPath, &plan); err != nil {
		return planner.Plan{}, err
	}
	return plan, nil
}

func RunApplyHelper(plan planner.Plan, stdout io.Writer, stderr io.Writer) error {
	inputPath, cleanupInput, err := writeTempJSON("plan-*.json", plan)
	if err != nil {
		return err
	}
	defer cleanupInput()

	return runSudoHelper([]string{"--internal-mode", ModeApply, "--internal-input", inputPath}, stdout, stderr)
}

func runSudoHelper(args []string, stdout io.Writer, stderr io.Writer) error {
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("sudo is required for privilege escalation; please install sudo: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}

	commandArgs := []string{"--prompt=dotfile-manager requires administrator privileges. Password: ", executable}
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(sudoPath, commandArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run privileged helper: %w", err)
	}
	return nil
}

func writeTempJSON(pattern string, value any) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func readJSON(path string, dest any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, dest); err != nil {
		return err
	}
	return nil
}
