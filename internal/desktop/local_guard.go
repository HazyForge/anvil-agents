package desktop

import (
	"net"
	"net/http"
	"path"
	"strings"
)

func (s *Server) localRequestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cleaned := path.Clean(request.URL.Path)
		if cleaned != "/local" && !strings.HasPrefix(cleaned, "/local/") {
			next.ServeHTTP(writer, request)
			return
		}
		listen := s.opts.Listen
		// The listener's address also covers ephemeral ports and embedded hosts.
		if addr, ok := request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			listen = addr.String()
		}
		_, port, err := net.SplitHostPort(listen)
		if err != nil || (request.Host != "127.0.0.1:"+port && request.Host != "localhost:"+port && request.Host != "[::1]:"+port) {
			writeError(writer, http.StatusForbidden, "local_request_required", "use the Desktop's loopback address and listening port")
			return
		}
		if origins, present := request.Header["Origin"]; present {
			if len(origins) != 1 || (origins[0] != "http://127.0.0.1:"+port && origins[0] != "http://localhost:"+port && origins[0] != "http://[::1]:"+port) {
				writeError(writer, http.StatusForbidden, "local_request_required", "local requests must come from the Desktop's own origin")
				return
			}
		}
		if sites, present := request.Header["Sec-Fetch-Site"]; present {
			if len(sites) != 1 || (sites[0] != "same-origin" && sites[0] != "none") {
				writeError(writer, http.StatusForbidden, "local_request_required", "cross-site requests to the Desktop are not allowed")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}
