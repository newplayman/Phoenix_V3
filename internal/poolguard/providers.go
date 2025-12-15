package poolguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Provider checks token risk via external service.
// In restricted network environments, these providers return ErrRemoteDisabled.
type Provider interface {
	Name() string
	CheckToken(ctx context.Context, chainID int64, token common.Address) (*PoolCheckResult, error)
}

var ErrRemoteDisabled = errors.New("poolguard remote provider disabled")

type GoPlusProvider struct {
	baseURL string
	keyEnv  string
	client  *http.Client
}

func NewGoPlusProvider(baseURL, keyEnv string) *GoPlusProvider {
	return &GoPlusProvider{
		baseURL: baseURL,
		keyEnv:  keyEnv,
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (p *GoPlusProvider) Name() string { return "goplus" }

func (p *GoPlusProvider) CheckToken(ctx context.Context, chainID int64, token common.Address) (*PoolCheckResult, error) {
	if p.baseURL == "" || os.Getenv(p.keyEnv) == "" {
		return nil, ErrRemoteDisabled
	}
	return fetchRisk(ctx, p.client, p.baseURL, os.Getenv(p.keyEnv), chainID, token, p.Name())
}

type HoneypotProvider struct {
	baseURL string
	keyEnv  string
	client  *http.Client
}

func NewHoneypotProvider(baseURL, keyEnv string) *HoneypotProvider {
	return &HoneypotProvider{
		baseURL: baseURL,
		keyEnv:  keyEnv,
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (p *HoneypotProvider) Name() string { return "honeypot" }

func (p *HoneypotProvider) CheckToken(ctx context.Context, chainID int64, token common.Address) (*PoolCheckResult, error) {
	if p.baseURL == "" || os.Getenv(p.keyEnv) == "" {
		return nil, ErrRemoteDisabled
	}
	return fetchRisk(ctx, p.client, p.baseURL, os.Getenv(p.keyEnv), chainID, token, p.Name())
}

func fetchRisk(ctx context.Context, client *http.Client, baseURL string, apiKey string, chainID int64, token common.Address, providerName string) (*PoolCheckResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	if baseURL == "" || apiKey == "" {
		return nil, ErrRemoteDisabled
	}
	endpoint := strings.ReplaceAll(baseURL, "{token}", token.Hex())
	endpoint = strings.ReplaceAll(endpoint, "{chain_id}", fmt.Sprintf("%d", chainID))
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid url: %w", providerName, err)
	}
	q := u.Query()
	if q.Get("token") == "" && q.Get("address") == "" {
		q.Set("token", token.Hex())
	}
	if chainID != 0 && q.Get("chain_id") == "" && q.Get("chainId") == "" {
		q.Set("chain_id", fmt.Sprintf("%d", chainID))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", providerName, err)
	}
	// Try common header conventions.
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", providerName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: status %s", providerName, resp.Status)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%s: decode json: %w", providerName, err)
	}
	res := parseGenericRiskResponse(providerName, token.Hex(), payload)
	return res, nil
}

func parseGenericRiskResponse(providerName, poolID string, payload map[string]interface{}) *PoolCheckResult {
	res := &PoolCheckResult{
		PoolID:      poolID,
		Risk:        RiskWarning,
		Reason:      providerName + ": unknown response",
		LastChecked: time.Now(),
	}
	if payload == nil {
		return res
	}
	if v, ok := payload["risk"].(string); ok {
		res.Risk = PoolRiskLevel(strings.ToLower(v))
	}
	if v, ok := payload["reason"].(string); ok && v != "" {
		res.Reason = providerName + ": " + v
	}
	if v, ok := payload["score"].(float64); ok {
		res.Score = v
	}
	if v, ok := payload["is_honeypot"].(bool); ok && v {
		res.Risk = RiskDanger
		res.Reason = providerName + ": honeypot"
	}
	if res.Risk != RiskSafe && res.Risk != RiskWarning && res.Risk != RiskDanger {
		res.Risk = RiskWarning
	}
	return res
}
