//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFullFlow(t *testing.T) {
	rootDir := repoRoot(t)

	requireCommand(t, "docker")

	composeUp := exec.Command("docker", "compose", "up", "-d", "postgres", "redis", "server", "relay")
	composeUp.Dir = rootDir
	composeUp.Stdout = os.Stdout
	composeUp.Stderr = os.Stderr
	if err := composeUp.Run(); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	t.Cleanup(func() {
		composeDown := exec.Command("docker", "compose", "down", "-v")
		composeDown.Dir = rootDir
		composeDown.Stdout = os.Stdout
		composeDown.Stderr = os.Stderr
		_ = composeDown.Run()
	})

	baseURL := "http://localhost:8080"
	waitForHealth(t, baseURL+"/health", 120*time.Second)

	registerPayload := map[string]any{
		"email":    fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano()),
		"password": "password123",
		"name":     "E2E User",
	}
	registerResponse := postJSON(t, baseURL+"/api/v1/auth/register", registerPayload, "")
	if !registerResponse.Success {
		t.Fatalf("register failed: %s", registerResponse.Error)
	}

	loginPayload := map[string]any{
		"email":    registerPayload["email"],
		"password": registerPayload["password"],
	}
	loginResponse := postJSON(t, baseURL+"/api/v1/auth/login", loginPayload, "")
	if !loginResponse.Success {
		t.Fatalf("login failed: %s", loginResponse.Error)
	}

	loginData := map[string]any{}
	mustDecodeData(t, loginResponse.Data, &loginData)
	accessToken := loginData["access_token"].(string)
	apiKey := loginData["api_key"].(string)

	networkPayload := map[string]any{
		"name":        "E2E Network",
		"description": "Integration test network",
	}
	networkResponse := postJSON(t, baseURL+"/api/v1/networks", networkPayload, accessToken)
	if !networkResponse.Success {
		t.Fatalf("create network failed: %s", networkResponse.Error)
	}

	networkData := map[string]any{}
	mustDecodeData(t, networkResponse.Data, &networkData)
	networkUUID := networkData["id"].(string)

	registerPeer := func(name string, machineID string, publicKey string) map[string]any {
		payload := map[string]any{
			"machine_id": machineID,
			"public_key": publicKey,
			"name":       name,
			"os":         "linux",
			"vnc_port":   5900,
		}
		resp := postJSONWithAPIKey(t, fmt.Sprintf("%s/api/v1/networks/%s/peers/register", baseURL, networkUUID), payload, apiKey)
		if !resp.Success {
			t.Fatalf("register peer %s failed: %s", name, resp.Error)
		}
		data := map[string]any{}
		mustDecodeData(t, resp.Data, &data)
		return data
	}

	peerA := registerPeer("Agent A", fmt.Sprintf("machine-a-%d", time.Now().UnixNano()), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	peerB := registerPeer("Agent B", fmt.Sprintf("machine-b-%d", time.Now().UnixNano()), "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=")

	heartbeat := func(peerID string, endpoint string) {
		payload := map[string]any{
			"public_endpoint": endpoint,
			"local_endpoints": []string{"192.168.1.2:51820"},
			"vnc_available":   true,
			"rx_bytes":        2048,
			"tx_bytes":        1024,
		}
		url := fmt.Sprintf("%s/api/v1/networks/%s/peers/%s/heartbeat", baseURL, networkUUID, peerID)
		resp := putJSONWithAPIKey(t, url, payload, apiKey)
		if !resp.Success {
			t.Fatalf("heartbeat failed for peer %s: %s", peerID, resp.Error)
		}
	}

	heartbeat(peerA["peer_id"].(string), "127.0.0.1:51820")
	heartbeat(peerB["peer_id"].(string), "127.0.0.1:51821")

	announce := func(peerID string, endpoint string) {
		payload := map[string]any{
			"peer_id":         peerID,
			"network_id":      networkUUID,
			"public_endpoint": endpoint,
			"local_endpoints": []string{"192.168.1.2:51820"},
		}
		resp := postJSONWithAPIKey(t, baseURL+"/api/v1/coord/announce", payload, apiKey)
		if !resp.Success {
			t.Fatalf("announce failed for peer %s: %s", peerID, resp.Error)
		}
	}

	announce(peerA["peer_id"].(string), "127.0.0.1:51820")
	announce(peerB["peer_id"].(string), "127.0.0.1:51821")

	coordPeersURL := fmt.Sprintf("%s/api/v1/coord/peers/%s?peer_id=%s", baseURL, networkUUID, peerA["peer_id"].(string))
	coordPeersResponse := getJSONWithAPIKey(t, coordPeersURL, apiKey)
	if !coordPeersResponse.Success {
		t.Fatalf("coord peers lookup failed: %s", coordPeersResponse.Error)
	}

	var peers []map[string]any
	mustDecodeData(t, coordPeersResponse.Data, &peers)
	if len(peers) == 0 {
		t.Fatalf("expected peer visibility between agents")
	}

	relayResponse := getJSONWithAPIKey(t, fmt.Sprintf("%s/api/v1/coord/relay/assign?peer_id=%s", baseURL, peerA["peer_id"].(string)), apiKey)
	if relayResponse.Success {
		var relayData map[string]any
		mustDecodeData(t, relayResponse.Data, &relayData)
		if relayData["relay_host"] == nil {
			t.Fatalf("relay assignment missing relay_host")
		}
	}

	t.Logf("e2e flow completed. Registered peers: %v, %v", peerA["peer_id"], peerB["peer_id"])
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func postJSON(t *testing.T, url string, payload any, token string) envelope {
	t.Helper()
	return doJSON(t, http.MethodPost, url, payload, token, "")
}

func postJSONWithAPIKey(t *testing.T, url string, payload any, apiKey string) envelope {
	t.Helper()
	return doJSON(t, http.MethodPost, url, payload, "", apiKey)
}

func putJSONWithAPIKey(t *testing.T, url string, payload any, apiKey string) envelope {
	t.Helper()
	return doJSON(t, http.MethodPut, url, payload, "", apiKey)
}

func getJSONWithAPIKey(t *testing.T, url string, apiKey string) envelope {
	t.Helper()
	return doJSON(t, http.MethodGet, url, nil, "", apiKey)
}

func doJSON(t *testing.T, method string, url string, payload any, token string, apiKey string) envelope {
	t.Helper()
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload failed: %v", err)
		}
		body = encoded
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var out envelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	return out
}

func mustDecodeData(t *testing.T, raw json.RawMessage, dest any) {
	t.Helper()
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("decode response data failed: %v", err)
	}
}

func waitForHealth(t *testing.T, healthURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("service did not become healthy: %s", healthURL)
}

func requireCommand(t *testing.T, command string) {
	t.Helper()
	if _, err := exec.LookPath(command); err != nil {
		t.Skipf("command not available: %s", command)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	candidate := wd
	for {
		if _, err := os.Stat(filepath.Join(candidate, "docker-compose.yml")); err == nil {
			return candidate
		}
		next := filepath.Dir(candidate)
		if next == candidate {
			t.Fatalf("could not locate repository root from %s", wd)
		}
		candidate = next
	}
}
