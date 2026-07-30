package runnercapabilities

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestInstallInlineScriptUsesInterpreterAndVerifyPreservesArgv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	script := `if [ "$1" != "space value" ]; then exit 10; fi
if [ "$2" != '$(touch ` + marker + `)' ]; then exit 11; fi
exit 0
`
	tool := structuredInlineTool(t, "inline", "inline", script)
	tool.Source.InlineScript.Interpreter = []string{"/bin/sh", "-e"}
	tool.VerifyCommand = []string{"inline", "space value", "$(touch " + marker + ")"}
	tool.SpecDigest, _ = ComputeToolSpecDigest(tool)

	root := t.TempDir()
	options := InstallOptions{
		CacheRoot: filepath.Join(root, "cache"), BinDir: filepath.Join(root, "bin"),
		Platform: Platform{OS: "linux", Arch: "amd64"},
	}
	installed, err := InstallTools(context.Background(), ToolManifest{tool}, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Reused {
		t.Fatalf("unexpected install result: %#v", installed)
	}
	if err := VerifyTools(context.Background(), ToolManifest{tool}, VerifyOptions{BinDir: options.BinDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("verify arguments were evaluated by a shell; marker err = %v", err)
	}
}

func TestInstallHTTPFormatsAndReuse(t *testing.T) {
	binary := []byte("#!/bin/sh\nexit 0\n")
	tarGz := makeTarGz(t, tar.Header{Name: "nested/tool", Mode: 0o644, Size: int64(len(binary)), Typeflag: tar.TypeReg}, binary)
	zipBody := makeZip(t, "nested/tool", 0o644, binary)

	for _, test := range []struct {
		name           string
		format         controlv1alpha1.AgentToolArchiveFormat
		body           []byte
		executablePath string
	}{
		{name: "binary", format: controlv1alpha1.AgentToolArchiveBinary, body: binary},
		{name: "targz", format: controlv1alpha1.AgentToolArchiveTarGZ, body: tarGz, executablePath: "nested/tool"},
		{name: "zip", format: controlv1alpha1.AgentToolArchiveZip, body: zipBody, executablePath: "nested/tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write(test.body)
			}))
			defer server.Close()
			tool := structuredHTTPTool(t, test.name, test.name, server.URL, test.body, test.format, test.executablePath)
			root := t.TempDir()
			options := InstallOptions{
				CacheRoot: filepath.Join(root, "cache"), BinDir: filepath.Join(root, "bin-1"),
				Platform: Platform{OS: "linux", Arch: "amd64"}, HTTPClient: server.Client(),
			}
			first, err := InstallTools(context.Background(), ToolManifest{tool}, options)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyTools(context.Background(), ToolManifest{tool}, VerifyOptions{BinDir: options.BinDir}); err != nil {
				t.Fatal(err)
			}
			options.BinDir = filepath.Join(root, "bin-2")
			second, err := InstallTools(context.Background(), ToolManifest{tool}, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(first) != 1 || len(second) != 1 || first[0].Reused || !second[0].Reused {
				t.Fatalf("unexpected reuse results: first=%#v second=%#v", first, second)
			}
		})
	}
}

func TestInstallDigestPinnedOCIArtifact(t *testing.T) {
	executable := []byte("#!/bin/sh\nexit 0\n")
	layer := makeTar(t, tar.Header{Name: "opt/tool", Mode: 0o644, Size: int64(len(executable)), Typeflag: tar.TypeReg}, executable)
	layerDigest := sha256Hex(layer)
	manifestBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     ociImageManifest,
		"layers": []map[string]any{{
			"mediaType": ociLayerTar, "digest": layerDigest, "size": len(layer),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256Hex(manifestBytes)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.Contains(request.URL.Path, "/manifests/"):
			_, _ = response.Write(manifestBytes)
		case strings.Contains(request.URL.Path, "/blobs/"):
			_, _ = response.Write(layer)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	reference := strings.TrimPrefix(server.URL, "https://") + "/team/tool@" + manifestDigest
	tool := Tool{
		Name: "oci", Description: "OCI tool",
		Executable: &controlv1alpha1.AgentToolExecutable{Name: "oci", Path: "bin/oci"},
		Source: &controlv1alpha1.AgentToolSource{OCIArtifact: &controlv1alpha1.AgentToolOCIArtifactSource{Artifacts: []controlv1alpha1.AgentToolOCIArtifact{{
			Platform: controlv1alpha1.AgentToolPlatform{OS: "linux", Arch: "amd64"}, Reference: reference, ExecutablePath: "opt/tool",
		}}}},
		VerifyCommand: []string{"oci", "--version"},
	}
	tool.SpecDigest, _ = ComputeToolSpecDigest(tool)
	root := t.TempDir()
	options := InstallOptions{
		CacheRoot: filepath.Join(root, "cache"), BinDir: filepath.Join(root, "bin"),
		Platform: Platform{OS: "linux", Arch: "amd64"}, HTTPClient: server.Client(),
	}
	if _, err := InstallTools(context.Background(), ToolManifest{tool}, options); err != nil {
		t.Fatal(err)
	}
	if err := VerifyTools(context.Background(), ToolManifest{tool}, VerifyOptions{BinDir: options.BinDir}); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	body := []byte("actual")
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(body)
	}))
	defer server.Close()
	tool := structuredHTTPTool(t, "bad", "bad", server.URL, []byte("expected"), controlv1alpha1.AgentToolArchiveBinary, "")
	root := t.TempDir()
	_, err := InstallTools(context.Background(), ToolManifest{tool}, InstallOptions{
		CacheRoot: filepath.Join(root, "cache"), BinDir: filepath.Join(root, "bin"),
		Platform: Platform{OS: "linux", Arch: "amd64"}, HTTPClient: server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum error = %v", err)
	}
}

func TestConcurrentCachePopulationPublishesCompleteWinner(t *testing.T) {
	body := []byte("#!/bin/sh\nexit 0\n")
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = response.Write(body)
	}))
	defer server.Close()
	tool := structuredHTTPTool(t, "concurrent", "concurrent", server.URL, body, controlv1alpha1.AgentToolArchiveBinary, "")
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	const workers = 12
	errorsByWorker := make([]error, workers)
	results := make([][]InstalledTool, workers)
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			results[index], errorsByWorker[index] = InstallTools(context.Background(), ToolManifest{tool}, InstallOptions{
				CacheRoot: cacheRoot, BinDir: filepath.Join(root, fmt.Sprintf("bin-%d", index)),
				Platform: Platform{OS: "linux", Arch: "amd64"}, HTTPClient: server.Client(),
			})
		}(index)
	}
	close(start)
	group.Wait()
	for index, err := range errorsByWorker {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
		if len(results[index]) != 1 {
			t.Fatalf("worker %d result = %#v", index, results[index])
		}
	}
	if requests.Load() < 1 {
		t.Fatal("artifact was never downloaded")
	}
	completion, err := validateCompletedCache(results[0][0].CachePath, tool.SpecDigest, "linux-amd64", tool.Executable.Path, "sha256:"+tool.Source.HTTPArtifact.Artifacts[0].SHA256)
	if err != nil || completion.ExecutablePath == "" {
		t.Fatalf("published cache is incomplete: completion=%#v err=%v", completion, err)
	}
}

func TestCacheTamperingAndIncompleteEntriesFailClosed(t *testing.T) {
	tool := structuredInlineTool(t, "cached", "cached", "exit 0\n")
	root := t.TempDir()
	options := InstallOptions{
		CacheRoot: filepath.Join(root, "cache"), BinDir: filepath.Join(root, "bin-1"),
		Platform: Platform{OS: "linux", Arch: "amd64"},
	}
	installed, err := InstallTools(context.Background(), ToolManifest{tool}, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed[0].ExecutablePath, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	options.BinDir = filepath.Join(root, "bin-2")
	if _, err := InstallTools(context.Background(), ToolManifest{tool}, options); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered cache error = %v", err)
	}

	other := structuredInlineTool(t, "incomplete", "incomplete", "exit 0\n")
	incomplete := filepath.Join(options.CacheRoot, other.Name, "linux-amd64", strings.Replace(other.SpecDigest, ":", "-", 1))
	if err := os.MkdirAll(incomplete, 0o755); err != nil {
		t.Fatal(err)
	}
	options.BinDir = filepath.Join(root, "bin-3")
	if _, err := InstallTools(context.Background(), ToolManifest{other}, options); err == nil {
		t.Fatal("expected incomplete cache entry to fail closed")
	}
}

func TestSafeExtractionRejectsUnsafeTarEntries(t *testing.T) {
	entries := []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Size: 1},
		{Name: "/absolute", Typeflag: tar.TypeReg, Size: 1},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"},
		{Name: "hardlink", Typeflag: tar.TypeLink, Linkname: "target"},
		{Name: "device", Typeflag: tar.TypeChar},
	}
	for _, header := range entries {
		t.Run(strings.ReplaceAll(header.Name, "/", "_"), func(t *testing.T) {
			body := []byte(nil)
			if header.Size > 0 {
				body = []byte("x")
			}
			archive := makeTarGz(t, header, body)
			path := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			if err := os.WriteFile(path, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractTarGz(path, t.TempDir(), extractionLimits{maxBytes: 1024, maxFiles: 10}); err == nil {
				t.Fatalf("entry %#v was accepted", header)
			}
		})
	}
}

func TestSafeExtractionRejectsUnsafeZipEntries(t *testing.T) {
	for _, test := range []struct {
		name string
		mode os.FileMode
	}{
		{name: "../escape", mode: 0o644},
		{name: "/absolute", mode: 0o644},
		{name: "link", mode: os.ModeSymlink | 0o777},
	} {
		t.Run(strings.ReplaceAll(test.name, "/", "_"), func(t *testing.T) {
			archive := makeZip(t, test.name, test.mode, []byte("target"))
			path := filepath.Join(t.TempDir(), "unsafe.zip")
			if err := os.WriteFile(path, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractZip(path, t.TempDir(), extractionLimits{maxBytes: 1024, maxFiles: 10}); err == nil {
				t.Fatalf("entry %#v was accepted", test)
			}
		})
	}
}

func makeTarGz(t *testing.T, header tar.Header, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if len(body) > 0 {
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeTar(t *testing.T, header tar.Header, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeZip(t *testing.T, name string, mode os.FileMode, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(entry, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
