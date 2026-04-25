package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	pkgcrypto "quicktunnel.local/pkg/crypto"
)

type JoinResponse struct {
	Status      string `json:"status"`
	MemberID    string `json:"member_id"`
	MemberToken string `json:"member_token"`
	VirtualIP   string `json:"virtual_ip"`
	NetworkCIDR string `json:"network_cidr"`
	NetworkName string `json:"network_name"`
}

// JoinNetwork sends a join request to the server and returns a Config.
func JoinNetwork(serverURL, networkID, configPath string) (*Config, error) {
	privateKey, publicKey, err := pkgcrypto.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate keys: %w", err)
	}

	machineID := pkgcrypto.MachineFingerprint()
	hostname, _ := os.Hostname()

	body := fmt.Sprintf(`{"public_key":"%s","machine_id":"%s","hostname":"%s"}`,
		publicKey, machineID, hostname)

	url := fmt.Sprintf("%s/api/v1/networks/%s/join", strings.TrimRight(serverURL, "/"), networkID)
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("join request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("join failed: HTTP %d", resp.StatusCode)
	}

	var jr JoinResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	cfg := &Config{
		ServerURL:    serverURL,
		NetworkID:    networkID,
		WGPrivateKey: privateKey,
		VirtualIP:    jr.VirtualIP,
		NetworkCIDR:  jr.NetworkCIDR,
		MemberID:     jr.MemberID,
		MemberToken:  jr.MemberToken,
		WGListenPort: 51820,
	}

	// Save config
	if configPath != "" {
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(configPath, data, 0600); err != nil {
			return nil, fmt.Errorf("save config: %w", err)
		}
	}

	return cfg, nil
}

// LoadFromFile loads config from a JSON file.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
