package runnercapabilities

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type extractionLimits struct {
	maxBytes int64
	maxFiles int
}

type extractionBudget struct {
	limits extractionLimits
	bytes  int64
	files  int
	seen   map[string]struct{}
}

func (budget *extractionBudget) reserve(name string, size int64) error {
	if size < 0 || size > budget.limits.maxBytes-budget.bytes {
		return fmt.Errorf("archive exceeds extracted size limit of %d bytes", budget.limits.maxBytes)
	}
	budget.files++
	if budget.files > budget.limits.maxFiles {
		return fmt.Errorf("archive exceeds file limit of %d", budget.limits.maxFiles)
	}
	if _, found := budget.seen[name]; found {
		return fmt.Errorf("archive contains duplicate path %q", name)
	}
	budget.seen[name] = struct{}{}
	budget.bytes += size
	return nil
}

func safeArchivePath(root, name string) (string, error) {
	if strings.Contains(name, `\`) || strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("archive path %q contains an unsafe character", name)
	}
	name = strings.TrimSuffix(name, "/")
	if name == cacheCompletionFile || name == inlineScriptFile {
		return "", fmt.Errorf("archive path %q is reserved", name)
	}
	if err := validateRelativePath(name); err != nil {
		return "", fmt.Errorf("archive path %q is unsafe: %w", name, err)
	}
	target := filepath.Join(root, filepath.FromSlash(name))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path %q escapes extraction root", name)
	}
	return target, nil
}

func extractTarGz(archivePath, root string, limits extractionLimits) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz artifact: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzipReader.Close()
	return extractTarReader(gzipReader, root, limits)
}

func extractTar(archivePath, root string, limits extractionLimits) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar artifact: %w", err)
	}
	defer file.Close()
	return extractTarReader(file, root, limits)
}

func extractTarReader(input io.Reader, root string, limits extractionLimits) error {
	budget := extractionBudget{limits: limits, seen: map[string]struct{}{}}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		target, err := safeArchivePath(root, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := budget.reserve(strings.TrimSuffix(header.Name, "/"), 0); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create archive directory: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := budget.reserve(header.Name, header.Size); err != nil {
				return err
			}
			if err := writeArchiveFile(target, io.LimitReader(reader, header.Size), header.Size, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("archive path %q uses a forbidden link entry", header.Name)
		default:
			return fmt.Errorf("archive path %q uses unsupported entry type %d", header.Name, header.Typeflag)
		}
	}
	return nil
}

func extractZip(archivePath, root string, limits extractionLimits) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip artifact: %w", err)
	}
	defer reader.Close()
	budget := extractionBudget{limits: limits, seen: map[string]struct{}{}}
	for _, entry := range reader.File {
		target, err := safeArchivePath(root, entry.Name)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path %q uses a forbidden symlink entry", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := budget.reserve(strings.TrimSuffix(entry.Name, "/"), 0); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create archive directory: %w", err)
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("archive path %q uses unsupported entry mode %s", entry.Name, mode)
		}
		if entry.UncompressedSize64 > uint64(limits.maxBytes) {
			return fmt.Errorf("archive path %q exceeds extracted size limit", entry.Name)
		}
		if err := budget.reserve(entry.Name, int64(entry.UncompressedSize64)); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open archive path %q: %w", entry.Name, err)
		}
		err = writeArchiveFile(target, input, int64(entry.UncompressedSize64), mode)
		closeErr := input.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close archive path %q: %w", entry.Name, closeErr)
		}
	}
	return nil
}

func writeArchiveFile(target string, reader io.Reader, expectedSize int64, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create archive parent: %w", err)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm()&0o777)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write archive file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close archive file: %w", closeErr)
	}
	if written != expectedSize {
		return fmt.Errorf("archive file size mismatch: wrote %d bytes, expected %d", written, expectedSize)
	}
	return nil
}
