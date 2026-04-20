package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestClientDownloadHandlerServesExistingBinary(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "client", "bin", "quicktunnel-linux-amd64")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("binary-data"), 0o755))

	handler := &ClientDownloadHandler{
		projectRoot: tempDir,
	}
	handler.buildBinary = func(spec clientBinarySpec, targetPath string) error {
		t.Fatalf("build should not run when artifact already exists")
		return nil
	}

	router := chi.NewRouter()
	router.Get("/api/v1/downloads/client/{os}/{arch}", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/client/linux/amd64", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "quicktunnel-linux-amd64")
	require.Equal(t, "binary-data", rec.Body.String())
}

func TestClientDownloadHandlerBuildsOnDemand(t *testing.T) {
	tempDir := t.TempDir()

	handler := &ClientDownloadHandler{
		projectRoot: tempDir,
		binaryDirs:  []string{filepath.Join(tempDir, "client", "bin")},
	}
	handler.buildBinary = func(spec clientBinarySpec, targetPath string) error {
		return os.WriteFile(targetPath, []byte("built-data"), 0o755)
	}

	router := chi.NewRouter()
	router.Get("/api/v1/downloads/client/{os}/{arch}", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/client/linux/amd64", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "built-data", rec.Body.String())
	require.FileExists(t, filepath.Join(tempDir, "client", "bin", "quicktunnel-linux-amd64"))
}

func TestClientDownloadHandlerServesRuntimeBinaryDir(t *testing.T) {
	tempDir := t.TempDir()
	runtimeDir := filepath.Join(tempDir, "client", "bin")
	target := filepath.Join(runtimeDir, "quicktunnel-linux-amd64")

	require.NoError(t, os.MkdirAll(runtimeDir, 0o755))
	require.NoError(t, os.WriteFile(target, []byte("runtime-data"), 0o755))

	handler := &ClientDownloadHandler{
		binaryDirs: []string{runtimeDir},
	}
	handler.buildBinary = func(spec clientBinarySpec, targetPath string) error {
		t.Fatalf("build should not run when runtime artifact already exists")
		return nil
	}

	router := chi.NewRouter()
	router.Get("/api/v1/downloads/client/{os}/{arch}", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/client/linux/amd64", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "runtime-data", rec.Body.String())
}

func TestClientDownloadHandlerRejectsUnsupportedPlatform(t *testing.T) {
	handler := &ClientDownloadHandler{}
	router := chi.NewRouter()
	router.Get("/api/v1/downloads/client/{os}/{arch}", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/client/linux/riscv64", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var payload responseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.False(t, payload.Success)
	require.Equal(t, "unsupported client platform", payload.Error)
}
