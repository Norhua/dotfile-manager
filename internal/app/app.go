package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"dotfile-manager/internal/config"
	"dotfile-manager/internal/executor"
	"dotfile-manager/internal/planner"
	"dotfile-manager/internal/privilege"
)

type options struct {
	ConfigPath     string
	Host           string
	Yes            bool
	InternalMode   string
	InternalInput  string
	InternalOutput string
}

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	switch opts.InternalMode {
	case privilege.ModePlan:
		return runInternalPlan(opts.InternalInput, opts.InternalOutput)
	case privilege.ModeApply:
		return runInternalApply(opts.InternalInput, stdout)
	case "":
		return runNormal(opts, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown internal mode %q", opts.InternalMode)
	}
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("dotfile-manager", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&opts.Host, "host", "", "override host name")
	fs.BoolVar(&opts.Yes, "yes", false, "apply without interactive confirmation")
	fs.StringVar(&opts.InternalMode, "internal-mode", "", "internal helper mode")
	fs.StringVar(&opts.InternalInput, "internal-input", "", "internal helper input file")
	fs.StringVar(&opts.InternalOutput, "internal-output", "", "internal helper output file")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if len(fs.Args()) > 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func runNormal(opts options, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	resolved, err := config.Load(config.LoadOptions{
		ConfigPath: opts.ConfigPath,
		Host:       opts.Host,
		Hostname:   hostname,
		HomeDir:    homeDir,
		Env:        currentEnv(),
	})
	if err != nil {
		return err
	}

	plan, err := planner.Build(resolved)
	if err != nil {
		var privilegeErr *planner.PrivilegeRequiredError
		if errors.As(err, &privilegeErr) && !privilege.IsRoot() {
			if !opts.Yes {
				ok, askErr := promptYesNo(stdin, stdout, fmt.Sprintf("Inspection needs administrator privileges for %s. Continue", privilegeErr.Path))
				if askErr != nil {
					return askErr
				}
				if !ok {
					return errors.New("aborted while requesting administrator privileges for scan")
				}
			}
			plan, err = privilege.RunPlanHelper(resolved, stdout, stderr)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	if _, err := io.WriteString(stdout, planner.Format(plan)); err != nil {
		return err
	}
	if len(plan.Actions) == 0 {
		return nil
	}

	if !opts.Yes {
		ok, err := promptYesNo(stdin, stdout, "Apply these changes")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted by user")
		}
	}

	if plan.RequiresExecutionPrivilege && !privilege.IsRoot() {
		return privilege.RunApplyHelper(plan, stdout, stderr)
	}
	return executor.Apply(plan, stdout)
}

func runInternalPlan(inputPath string, outputPath string) error {
	if inputPath == "" || outputPath == "" {
		return errors.New("internal plan mode requires input and output paths")
	}
	var resolved config.Resolved
	if err := readJSON(inputPath, &resolved); err != nil {
		return err
	}
	plan, err := planner.Build(resolved)
	if err != nil {
		return err
	}
	return writeJSON(outputPath, plan)
}

func runInternalApply(inputPath string, stdout io.Writer) error {
	if inputPath == "" {
		return errors.New("internal apply mode requires an input path")
	}
	var plan planner.Plan
	if err := readJSON(inputPath, &plan); err != nil {
		return err
	}
	return executor.Apply(plan, stdout)
}

func promptYesNo(stdin io.Reader, stdout io.Writer, question string) (bool, error) {
	reader := bufio.NewReader(stdin)
	for {
		if _, err := fmt.Fprintf(stdout, "%s [y/N]: ", question); err != nil {
			return false, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		}
	}
}

func currentEnv() map[string]string {
	result := map[string]string{}
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func readJSON(path string, dest any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, dest)
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}
