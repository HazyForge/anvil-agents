package runnercapabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	ociImageManifest    = "application/vnd.oci.image.manifest.v1+json"
	dockerImageManifest = "application/vnd.docker.distribution.manifest.v2+json"
	ociLayerTar         = "application/vnd.oci.image.layer.v1.tar"
	ociLayerTarGzip     = "application/vnd.oci.image.layer.v1.tar+gzip"
	dockerLayerTarGzip  = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	ociMaxManifestBytes = int64(4 << 20)
)

type parsedOCIReference struct {
	registry   string
	repository string
	digest     string
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Layers        []ociDescriptor `json:"layers"`
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func validateOCIReference(reference string) error {
	_, err := parseOCIReference(reference)
	return err
}

func parseOCIReference(reference string) (parsedOCIReference, error) {
	name, digest, found := strings.Cut(reference, "@")
	if !found || !validPrefixedSHA256(digest) || strings.Contains(name, "@") {
		return parsedOCIReference{}, errors.New("must contain exactly one @sha256:<lowercase digest>")
	}
	registry, repository, found := strings.Cut(name, "/")
	if !found || registry == "" || repository == "" {
		return parsedOCIReference{}, errors.New("must include an explicit registry and repository")
	}
	registryURL, err := url.Parse("https://" + registry)
	if err != nil || registryURL.Host != registry || registryURL.Path != "" || registryURL.User != nil {
		return parsedOCIReference{}, errors.New("contains an invalid registry")
	}
	for _, segment := range strings.Split(repository, "/") {
		if !safeNamePattern.MatchString(segment) {
			return parsedOCIReference{}, errors.New("contains an invalid repository path")
		}
	}
	return parsedOCIReference{registry: registry, repository: repository, digest: digest}, nil
}

func acquireOCIArtifact(ctx context.Context, artifact *controlv1alpha1.AgentToolOCIArtifact, staging, downloadDir string, options InstallOptions) (string, string, error) {
	reference, err := parseOCIReference(artifact.Reference)
	if err != nil {
		return "", "", err
	}
	base := "https://" + reference.registry + "/v2/" + reference.repository
	manifestURL := base + "/manifests/" + reference.digest
	manifestBytes, err := fetchOCIBytes(ctx, options.HTTPClient, manifestURL, ociImageManifest+", "+dockerImageManifest, ociMaxManifestBytes)
	if err != nil {
		return "", "", fmt.Errorf("fetch OCI manifest: %w", err)
	}
	if got := sha256Hex(manifestBytes); got != reference.digest {
		return "", "", fmt.Errorf("OCI manifest digest mismatch: got %s", got)
	}
	var manifest ociManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", "", fmt.Errorf("decode OCI manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 || (manifest.MediaType != "" && manifest.MediaType != ociImageManifest && manifest.MediaType != dockerImageManifest) {
		return "", "", errors.New("OCI reference does not resolve to a supported image manifest")
	}
	if len(manifest.Layers) != 1 {
		return "", "", fmt.Errorf("OCI tool artifact must contain exactly one filesystem layer, got %d", len(manifest.Layers))
	}
	layer := manifest.Layers[0]
	if !validPrefixedSHA256(layer.Digest) || layer.Size < 0 || layer.Size > options.MaxDownloadBytes {
		return "", "", errors.New("OCI layer descriptor is invalid or exceeds the download limit")
	}
	switch layer.MediaType {
	case ociLayerTar, ociLayerTarGzip, dockerLayerTarGzip:
	default:
		return "", "", fmt.Errorf("OCI layer media type %q is unsupported", layer.MediaType)
	}

	layerBytes, err := fetchOCIBytes(ctx, options.HTTPClient, base+"/blobs/"+layer.Digest, layer.MediaType, options.MaxDownloadBytes)
	if err != nil {
		return "", "", fmt.Errorf("fetch OCI layer: %w", err)
	}
	if int64(len(layerBytes)) != layer.Size {
		return "", "", fmt.Errorf("OCI layer size mismatch: got %d, want %d", len(layerBytes), layer.Size)
	}
	if got := sha256Hex(layerBytes); got != layer.Digest {
		return "", "", fmt.Errorf("OCI layer digest mismatch: got %s", got)
	}
	layerFile, err := os.CreateTemp(downloadDir, ".oci-layer-")
	if err != nil {
		return "", "", err
	}
	layerPath := layerFile.Name()
	defer os.Remove(layerPath)
	if _, err := layerFile.Write(layerBytes); err != nil {
		_ = layerFile.Close()
		return "", "", err
	}
	if err := layerFile.Close(); err != nil {
		return "", "", err
	}
	limits := extractionLimits{maxBytes: options.MaxExtractBytes, maxFiles: options.MaxExtractFiles}
	if layer.MediaType == ociLayerTar {
		if err := extractTar(layerPath, staging, limits); err != nil {
			return "", "", err
		}
	} else if err := extractTarGz(layerPath, staging, limits); err != nil {
		return "", "", err
	}
	executable := filepath.Join(staging, filepath.FromSlash(artifact.ExecutablePath))
	// #nosec G302 -- this is the selected tool executable, not sensitive data;
	// execution by the container user requires executable mode bits.
	if err := os.Chmod(executable, 0o755); err != nil {
		return "", "", fmt.Errorf("mark OCI executable: %w", err)
	}
	return artifact.ExecutablePath, reference.digest, nil
}

func fetchOCIBytes(ctx context.Context, client *http.Client, endpoint, accept string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("WWW-Authenticate")
		_ = response.Body.Close()
		token, tokenErr := anonymousBearerToken(ctx, client, challenge)
		if tokenErr != nil {
			return nil, tokenErr
		}
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", accept)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err = client.Do(request)
		if err != nil {
			return nil, err
		}
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" {
		return nil, errors.New("OCI request was redirected away from HTTPS")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OCI registry returned HTTP status %d", response.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("OCI response exceeds %d bytes", limit)
	}
	return contents, nil
}

func anonymousBearerToken(ctx context.Context, client *http.Client, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("OCI registry requires unsupported authentication")
	}
	parameters := map[string]string{}
	for _, item := range strings.Split(strings.TrimSpace(challenge[len("Bearer "):]), ",") {
		name, value, found := strings.Cut(strings.TrimSpace(item), "=")
		if found {
			parameters[strings.ToLower(name)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	realm, err := url.Parse(parameters["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" || realm.User != nil {
		return "", errors.New("OCI bearer challenge has an invalid token realm")
	}
	query := realm.Query()
	for _, name := range []string{"service", "scope"} {
		if value := parameters[name]; value != "" {
			query.Set(name, value)
		}
	}
	realm.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("request OCI bearer token")
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("OCI bearer token request failed")
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		return "", errors.New("decode OCI bearer token")
	}
	token := payload.Token
	if token == "" {
		token = payload.AccessToken
	}
	if token == "" || strings.IndexByte(token, 0) >= 0 {
		return "", errors.New("OCI bearer token response is empty")
	}
	return token, nil
}
