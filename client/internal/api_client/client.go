package api_client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	requestTimeout = 10 * time.Second
	maxRetries     = 3
)

// Client handles communication with the coordination server API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     zerolog.Logger

	mu          sync.RWMutex
	apiKey      string
	token       string
	memberToken string // ZeroTier-style member_token
}

// HTTPError wraps non-successful API responses.
type HTTPError struct {
	StatusCode int
	Message    string
	Body       string
	Temporary  bool
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("http error: status=%d message=%s", e.StatusCode, e.Message)
}

// AuthResponse contains login/session material for API callers.
type AuthResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	APIKey       string   `json:"api_key"`
	User         UserInfo `json:"user"`
}

// UserInfo is returned by auth endpoints.
type UserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// PeerRegisterRequest is the payload for machine registration.
type PeerRegisterRequest struct {
	MachineID string `json:"machine_id"`
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
	OS        string `json:"os"`
	Version   string `json:"version"`
	VNCPort   int    `json:"vnc_port"`
}

// PeerInfo represents peer metadata returned by coordination APIs.
type PeerInfo struct {
	ID             string   `json:"id"`
	NetworkID      string   `json:"network_id"`
	Name           string   `json:"name"`
	MachineID      string   `json:"machine_id"`
	PublicKey      string   `json:"public_key"`
	VirtualIP      string   `json:"virtual_ip"`
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
	OS             string   `json:"os"`
	Version        string   `json:"version"`
	IsOnline       bool     `json:"is_online"`
	VNCPort        int      `json:"vnc_port"`
	VNCAvailable   bool     `json:"vnc_available"`
	RelayID        string   `json:"relay_id"`
}

// PeerRegistrationResult is returned when a peer is registered.
type PeerRegistrationResult struct {
	VirtualIP   string `json:"virtual_ip"`
	NetworkCIDR string `json:"network_cidr"`
	PeerID      string `json:"peer_id"`
}

// PeerStatus carries heartbeat updates to the server.
type PeerStatus struct {
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
	VNCAvailable   bool     `json:"vnc_available"`
	RXBytes        int64    `json:"rx_bytes"`
	TXBytes        int64    `json:"tx_bytes"`
	RelayID        string   `json:"relay_id"`
}

// AnnounceRequest advertises endpoints for coordination.
type AnnounceRequest struct {
	PeerID         string   `json:"peer_id"`
	NetworkID      string   `json:"network_id"`
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
}

// RelayInfo describes relay fallback assignment details.
type RelayInfo struct {
	PeerID    string `json:"peer_id"`
	RelayID   string `json:"relay_id"`
	RelayHost string `json:"relay_host"`
	RelayPort int    `json:"relay_port"`
	Token     string `json:"token"`
	Region    string `json:"region"`
}

type responseEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

type requestAuthMode int

const (
	authNone requestAuthMode = iota
	authJWT
	authAPIKey
	authMemberToken // ZeroTier-style: Bearer <member_token>
)

// NewClient creates a coordination API client with sane defaults.
func NewClient(serverURL, apiKey string) *Client {
	trimmedURL := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	return &Client{
		baseURL: trimmedURL,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
		logger: log.With().Str("component", "api_client").Logger(),
		apiKey: strings.TrimSpace(apiKey),
	}
}

// SetToken updates bearer token used for authenticated user calls.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = strings.TrimSpace(token)
}

// SetAPIKey updates API key used for machine-authenticated calls.
func (c *Client) SetAPIKey(apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = strings.TrimSpace(apiKey)
}

// Login authenticates user credentials and stores resulting session credentials.
func (c *Client) Login(email, password string) (*AuthResponse, error) {
	payload := map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	}

	var out AuthResponse
	if err := c.doJSON(context.Background(), http.MethodPost, "/api/v1/auth/login", payload, authNone, &out); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	c.SetToken(out.AccessToken)
	if out.APIKey != "" {
		c.SetAPIKey(out.APIKey)
	}

	return &out, nil
}

// RegisterPeer registers the current machine in a network.
func (c *Client) RegisterPeer(networkID string, req PeerRegisterRequest) (*PeerRegistrationResult, error) {
	path := fmt.Sprintf("/api/v1/networks/%s/peers/register", url.PathEscape(networkID))
	var out PeerRegistrationResult
	if err := c.doJSON(context.Background(), http.MethodPost, path, req, authAPIKey, &out); err != nil {
		return nil, fmt.Errorf("register peer: %w", err)
	}
	return &out, nil
}

// Heartbeat updates peer liveness and transport counters.
func (c *Client) Heartbeat(networkID, peerID string, status PeerStatus) error {
	path := fmt.Sprintf("/api/v1/networks/%s/peers/%s/heartbeat", url.PathEscape(networkID), url.PathEscape(peerID))
	if err := c.doJSON(context.Background(), http.MethodPut, path, status, authAPIKey, nil); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}

// GetPeers fetches currently online peers for a network from coordination endpoint.
func (c *Client) GetPeers(networkID string) ([]PeerInfo, error) {
	path := fmt.Sprintf("/api/v1/coord/peers/%s", url.PathEscape(networkID))
	var out []PeerInfo
	if err := c.doJSON(context.Background(), http.MethodGet, path, nil, authAPIKey, &out); err != nil {
		return nil, fmt.Errorf("get peers: %w", err)
	}
	return out, nil
}

// Announce posts latest public/local endpoint information for this peer.
func (c *Client) Announce(endpoint AnnounceRequest) error {
	if err := c.doJSON(context.Background(), http.MethodPost, "/api/v1/coord/announce", endpoint, authAPIKey, nil); err != nil {
		return fmt.Errorf("announce: %w", err)
	}
	return nil
}

// GetNearestRelay requests relay fallback assignment for a peer.
func (c *Client) GetNearestRelay(peerID string) (*RelayInfo, error) {
	query := url.Values{}
	query.Set("peer_id", peerID)
	path := "/api/v1/coord/relay/assign?" + query.Encode()

	var out RelayInfo
	if err := c.doJSON(context.Background(), http.MethodGet, path, nil, authAPIKey, &out); err != nil {
		return nil, fmt.Errorf("get nearest relay: %w", err)
	}
	return &out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, requestBody any, authMode requestAuthMode, responseTarget any) error {
	fullURL := c.baseURL + path

	var bodyBytes []byte
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("do json: marshal request: %w", err)
		}
		bodyBytes = encoded
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("do json: context canceled during retry backoff: %w", ctx.Err())
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		req, err := http.NewRequestWithContext(reqCtx, method, fullURL, bytes.NewReader(bodyBytes))
		if err != nil {
			cancel()
			return fmt.Errorf("do json: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		if err := c.applyAuth(req, authMode); err != nil {
			cancel()
			return fmt.Errorf("do json: apply auth: %w", err)
		}

		c.logger.Debug().
			Str("method", method).
			Str("url", fullURL).
			Int("attempt", attempt+1).
			Msg("sending api request")

		resp, err := c.httpClient.Do(req)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("do json: perform request: %w", err)
			c.logger.Debug().Err(lastErr).Str("url", fullURL).Msg("api request failed")
			continue
		}

		responseData, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("do json: read response body: %w", readErr)
			continue
		}

		c.logger.Debug().
			Str("method", method).
			Str("url", fullURL).
			Int("status", resp.StatusCode).
			Int("response_bytes", len(responseData)).
			Msg("received api response")

		envelope := responseEnvelope{}
		if len(responseData) > 0 {
			if err := json.Unmarshal(responseData, &envelope); err != nil {
				if resp.StatusCode >= 500 && attempt < maxRetries-1 {
					lastErr = fmt.Errorf("do json: decode envelope: %w", err)
					continue
				}
				return fmt.Errorf("do json: decode envelope: %w", err)
			}
		}

		if resp.StatusCode >= http.StatusBadRequest || !envelope.Success {
			errMsg := envelope.Error
			if errMsg == "" {
				errMsg = strings.TrimSpace(string(responseData))
				if errMsg == "" {
					errMsg = http.StatusText(resp.StatusCode)
				}
			}
			httpErr := &HTTPError{
				StatusCode: resp.StatusCode,
				Message:    errMsg,
				Body:       strings.TrimSpace(string(responseData)),
				Temporary:  resp.StatusCode >= 500,
			}

			if httpErr.Temporary && attempt < maxRetries-1 {
				lastErr = httpErr
				continue
			}
			return httpErr
		}

		if responseTarget != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			if err := json.Unmarshal(envelope.Data, responseTarget); err != nil {
				return fmt.Errorf("do json: decode response data: %w", err)
			}
		}
		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("do json: request failed after retries")
	}
	return lastErr
}

func (c *Client) applyAuth(req *http.Request, mode requestAuthMode) error {
	c.mu.RLock()
	token := c.token
	apiKey := c.apiKey
	c.mu.RUnlock()

	switch mode {
	case authNone:
		return nil
	case authJWT:
		if token == "" {
			return fmt.Errorf("missing jwt token")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	case authAPIKey:
		if apiKey == "" {
			return fmt.Errorf("missing api key")
		}
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		return nil
	case authMemberToken:
		c.mu.RLock()
		mt := c.memberToken
		c.mu.RUnlock()
		if mt == "" {
			return fmt.Errorf("missing member_token")
		}
		req.Header.Set("Authorization", "Bearer "+mt)
		return nil
	default:
		return fmt.Errorf("unknown auth mode")
	}
}

// SetMemberToken sets the ZeroTier-style member token for unauthenticated tunnel endpoints.
func (c *Client) SetMemberToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.memberToken = strings.TrimSpace(token)
}

// MemberHeartbeatRequest is sent by ZeroTier-style peers every 30s.
type MemberHeartbeatRequest struct {
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
	VNCAvailable   bool     `json:"vnc_available"`
	RXBytes        int64    `json:"rx_bytes"`
	TXBytes        int64    `json:"tx_bytes"`
}

// MemberAnnounceRequest publishes the peer endpoint to Redis.
type MemberAnnounceRequest struct {
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
}

// MemberHeartbeat sends a heartbeat using member_token auth.
func (c *Client) MemberHeartbeat(memberID string, req MemberHeartbeatRequest) error {
	path := fmt.Sprintf("/api/v1/members/%s/heartbeat", url.PathEscape(memberID))
	if err := c.doJSON(context.Background(), http.MethodPut, path, req, authMemberToken, nil); err != nil {
		return fmt.Errorf("member heartbeat: %w", err)
	}
	return nil
}

// MemberGetPeers returns online peers in the same network using member_token auth.
func (c *Client) MemberGetPeers(memberID string) ([]PeerInfo, error) {
	path := fmt.Sprintf("/api/v1/members/%s/peers", url.PathEscape(memberID))
	var out []PeerInfo
	if err := c.doJSON(context.Background(), http.MethodGet, path, nil, authMemberToken, &out); err != nil {
		return nil, fmt.Errorf("member get peers: %w", err)
	}
	return out, nil
}

// MemberAnnounce publishes endpoint to Redis using member_token auth.
func (c *Client) MemberAnnounce(memberID string, req MemberAnnounceRequest) error {
	path := fmt.Sprintf("/api/v1/members/%s/announce", url.PathEscape(memberID))
	if err := c.doJSON(context.Background(), http.MethodPost, path, req, authMemberToken, nil); err != nil {
		return fmt.Errorf("member announce: %w", err)
	}
	return nil
}

// MemberGoOffline signals the server that this member is disconnecting.
func (c *Client) MemberGoOffline(memberID string) error {
	path := fmt.Sprintf("/api/v1/members/%s/offline", url.PathEscape(memberID))
	if err := c.doJSON(context.Background(), http.MethodPost, path, nil, authMemberToken, nil); err != nil {
		return fmt.Errorf("member go offline: %w", err)
	}
	return nil
}