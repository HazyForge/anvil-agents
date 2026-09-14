//go:build ignore

// Anvil Agents Desktop per-user Windows installer.
// Built by hack/package-anvil-desktop.sh with GOOS=windows. Not part of go test ./...
package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed payload.zip
var payload []byte

const productTitle = "Anvil Agents Desktop"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Fprintf(os.Stderr, "Install %s for the current user.\n", productTitle)
		fmt.Fprintf(os.Stderr, "Usage: Anvil-Agents-Desktop-Setup.exe [--uninstall]\n")
		os.Exit(0)
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if local == "" {
		return fmt.Errorf("LOCALAPPDATA is not set")
	}
	dest := filepath.Join(local, "Programs", "AnvilAgentsDesktop")
	uninstall := false
	for _, arg := range args {
		if strings.EqualFold(arg, "--uninstall") || strings.EqualFold(arg, "/uninstall") {
			uninstall = true
		}
	}
	if uninstall {
		_ = os.Remove(shortcutPath())
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", dest)
		return nil
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if err := unzipPayload(dest); err != nil {
		return err
	}
	exe := filepath.Join(dest, "anvil-desktop.exe")
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("anvil-desktop.exe missing after extract: %w", err)
	}
	if err := writeShortcut(exe, dest); err != nil {
		fmt.Fprintf(os.Stderr, "warning: start menu shortcut: %s\n", err)
	}
	fmt.Printf("Installed %s to %s\n", productTitle, dest)
	fmt.Println("Start Menu: Anvil Agents Desktop")
	fmt.Println("The host listens on http://127.0.0.1:1738")
	fmt.Println("This process signs in to Primaris agents (Chat / Wrapper).")
	fmt.Println("Local is a second page for already-installed grok, Codex, and OpenCode.")
	return nil
}

func unzipPayload(dest string) error {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return fmt.Errorf("payload zip: %w", err)
	}
	for _, file := range reader.File {
		name := filepath.Clean(file.Name)
		if name == "." || strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(dest, name) // #nosec G305 -- name is cleaned; dest is LOCALAPPDATA
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			continue
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = src.Close()
			return err
		}
		if _, err := io.Copy(out, src); err != nil {
			_ = src.Close()
			_ = out.Close()
			return err
		}
		_ = src.Close()
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

func shortcutPath() string {
	programs := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")
	return filepath.Join(programs, productTitle+".lnk")
}

func writeShortcut(exe, workdir string) error {
	link := shortcutPath()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	script := fmt.Sprintf(
		"$s = (New-Object -ComObject WScript.Shell).CreateShortcut(%s); $s.TargetPath = %s; $s.Arguments = '--open'; $s.WorkingDirectory = %s; $s.Description = %s; $s.Save()",
		psQuote(shortcutPath()),
		psQuote(exe),
		psQuote(workdir),
		psQuote(productTitle),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script) // #nosec G204 -- argv is generated from LOCALAPPDATA paths via psQuote
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
