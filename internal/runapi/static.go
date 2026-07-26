package runapi

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/hazyforge/anvil-agents/internal/runapi/consolefs"
)

const (
	uiIndexFile = "index.html"
	uiDistRoot  = "dist"
)

func (server *Server) uiFileSystem() (http.FileSystem, error) {
	if dir := strings.TrimSpace(server.config.UI.StaticDir); dir != "" {
		return http.FS(os.DirFS(dir)), nil
	}
	sub, err := fs.Sub(consolefs.Dist, uiDistRoot)
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

func (server *Server) handleUI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeAPIError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are supported")
		return
	}
	if isReservedUIPath(request.URL.Path) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}

	root, err := server.uiFileSystem()
	if err != nil {
		server.log.Error(err, "open console assets")
		writeAPIError(writer, http.StatusServiceUnavailable, "ui_unavailable", "console assets are unavailable")
		return
	}

	requestPath := cleanUIPath(request.URL.Path)
	servePath := requestPath
	if requestPath == "" || requestPath == "." {
		servePath = uiIndexFile
	}

	if file, err := root.Open(servePath); err == nil {
		info, statErr := file.Stat()
		if statErr == nil && !info.IsDir() {
			serveUIFile(writer, request, servePath, info, file)
			return
		}
		_ = file.Close()
	}

	// SPA client-side routes fall back to index.html when the path has no
	// file extension (so missing hashed assets still 404).
	if path.Ext(servePath) != "" {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}

	index, err := root.Open(uiIndexFile)
	if err != nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "ui_unavailable", "console index is unavailable")
		return
	}
	info, err := index.Stat()
	if err != nil {
		_ = index.Close()
		writeAPIError(writer, http.StatusServiceUnavailable, "ui_unavailable", "console index is unavailable")
		return
	}
	serveUIFile(writer, request, uiIndexFile, info, index)
}

func serveUIFile(writer http.ResponseWriter, request *http.Request, name string, info fs.FileInfo, file http.File) {
	defer file.Close()
	if strings.HasSuffix(name, ".html") {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	http.ServeContent(writer, request, name, info.ModTime(), file)
}

func cleanUIPath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	// Prevent path traversal after Clean (e.g. encoded segments).
	if strings.Contains(cleaned, "..") {
		return ""
	}
	return cleaned
}

func isReservedUIPath(raw string) bool {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	if cleaned == "/healthz" || cleaned == "/readyz" {
		return true
	}
	return strings.HasPrefix(cleaned, "/api/") || cleaned == "/api"
}

func isAPIOrProbePath(raw string) bool {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	if cleaned == "/healthz" || cleaned == "/readyz" {
		return true
	}
	return strings.HasPrefix(cleaned, "/api/") || cleaned == "/api"
}
