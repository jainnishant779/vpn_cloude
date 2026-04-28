package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestClientDownloadHandlerPrefersNewestArtifact(t *testing.T) {
	tempDir := t.TempDir()
	oldDir := filepath.Join(tempDir, "old", "bin")
	newDir := filepath.Join(tempDir, "new", "bin")
	require.NoError(t, os.MkdirAll(oldDir, 0o755))
	require.NoError(t, os.MkdirAll(newDir, 0o755))

	oldPath := filepath.Join(oldDir, "quicktunnel-linux-amd64")
	newPath := filepath.Join(newDir, "quicktunnel-linux-amd64")
	require.NoError(t, os.WriteFile(oldPath, []byte("old-data"), 0o755))
	require.NoError(t, os.WriteFile(newPath, []byte("new-data"), 0o755))

	now := time.Now()
	require.NoError(t, os.Chtimes(oldPath, now.Add(-1*time.Hour), now.Add(-1*time.Hour)))
	require.NoError(t, os.Chtimes(newPath, now, now))

	handler := &ClientDownloadHandler{
		binaryDirs: []string{oldDir, newDir},
	}
	handler.buildBinary = func(spec clientBinarySpec, targetPath string) error {
		t.Fatalf("build should not run when artifacts already exist")
		return nil
	}

	router := chi.NewRouter()
	router.Get("/api/v1/downloads/client/{os}/{arch}", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/client/linux/amd64", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "new-data", rec.Body.String())
}
