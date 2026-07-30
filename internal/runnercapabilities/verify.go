package runnercapabilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type VerifyOptions struct {
	BinDir string
	Dir    string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

// VerifyTools runs each verifyCommand directly as an argv array. It never
// invokes a shell and fails closed if a command cannot be resolved or exits
// unsuccessfully.
func VerifyTools(ctx context.Context, manifest ToolManifest, options VerifyOptions) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if options.BinDir == "" || !filepath.IsAbs(options.BinDir) {
		return errors.New("tool bin directory must be an absolute path")
	}
	environment := options.Env
	if environment == nil {
		environment = os.Environ()
	} else {
		environment = append([]string(nil), environment...)
	}
	pathValue := environmentValue(environment, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	pathValue = options.BinDir + string(os.PathListSeparator) + pathValue
	environment = replaceEnvironment(environment, "PATH", pathValue)

	stdout, stderr := options.Stdout, options.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	for _, tool := range manifest {
		if len(tool.VerifyCommand) == 0 {
			continue
		}
		resolved, err := lookPath(tool.VerifyCommand[0], pathValue)
		if err != nil {
			return fmt.Errorf("verify tool %q: command is unavailable", tool.Name)
		}
		command := exec.CommandContext(ctx, resolved, tool.VerifyCommand[1:]...)
		command.Args[0] = tool.VerifyCommand[0]
		command.Dir = options.Dir
		command.Env = environment
		command.Stdin = nil
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("verify tool %q: %w", tool.Name, ctx.Err())
			}
			return fmt.Errorf("verify tool %q failed", tool.Name)
		}
	}
	return nil
}

func lookPath(command, pathValue string) (string, error) {
	if strings.ContainsRune(command, filepath.Separator) {
		if !filepath.IsAbs(command) {
			return "", errors.New("verify command paths must be absolute")
		}
		if err := requireRunnable(command); err != nil {
			return "", err
		}
		return command, nil
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, command)
		if err := requireRunnable(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func requireRunnable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("command is not an executable regular file")
	}
	return nil
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func replaceEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
