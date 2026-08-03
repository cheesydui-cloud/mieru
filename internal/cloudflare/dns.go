// Package cloudflare provides a minimal DNS record helper for Cloudflare API v4.
// Used by the panel to create/update A/AAAA records when operators add a node domain.
package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// Client talks to Cloudflare with an API Token (Bearer).
type Client struct {
	Token  string
	ZoneID string
	HTTP   *http.Client
}

func New(token, zoneID string) *Client {
	return &Client{
		Token:  strings.TrimSpace(token),
		ZoneID: strings.TrimSpace(zoneID),
		HTTP:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) ok() error {
	if c == nil || c.Token == "" {
		return fmt.Errorf("未配置 Cloudflare API Token（设置页）")
	}
	if c.ZoneID == "" {
		return fmt.Errorf("未配置 Cloudflare Zone ID（设置页）")
	}
	return nil
}

type apiResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// UpsertA creates or updates an A/AAAA record for name → ip.
// proxied=false (DNS only) is required for custom ports / non-HTTP mieru entry.
func (c *Client) UpsertA(name, ip string, proxied bool) (*DNSRecord, error) {
	if err := c.ok(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimSuffix(name, ".")
	ip = strings.TrimSpace(ip)
	if name == "" {
		return nil, fmt.Errorf("域名不能为空")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil, fmt.Errorf("无效 IP: %s", ip)
	}
	recType := "A"
	if parsed.To4() == nil {
		recType = "AAAA"
	}
	// list existing same type+name
	existing, err := c.listByName(recType, name)
	if err != nil {
		return nil, err
	}
	body := DNSRecord{
		Type:    recType,
		Name:    name,
		Content: ip,
		TTL:     1, // auto
		Proxied: proxied,
	}
	if len(existing) > 0 {
		// update first match
		id := existing[0].ID
		return c.putRecord(id, body)
	}
	return c.postRecord(body)
}

func (c *Client) listByName(recType, name string) ([]DNSRecord, error) {
	u := fmt.Sprintf("%s/zones/%s/dns_records?type=%s&name=%s&per_page=50",
		apiBase, c.ZoneID, recType, name)
	var out struct {
		apiResp
		Result []DNSRecord `json:"result"`
	}
	if err := c.do("GET", u, nil, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, apiErr(out.Errors)
	}
	return out.Result, nil
}

func (c *Client) postRecord(rec DNSRecord) (*DNSRecord, error) {
	u := fmt.Sprintf("%s/zones/%s/dns_records", apiBase, c.ZoneID)
	var out struct {
		apiResp
		Result DNSRecord `json:"result"`
	}
	if err := c.do("POST", u, rec, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, apiErr(out.Errors)
	}
	return &out.Result, nil
}

func (c *Client) putRecord(id string, rec DNSRecord) (*DNSRecord, error) {
	u := fmt.Sprintf("%s/zones/%s/dns_records/%s", apiBase, c.ZoneID, id)
	var out struct {
		apiResp
		Result DNSRecord `json:"result"`
	}
	if err := c.do("PUT", u, rec, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, apiErr(out.Errors)
	}
	return &out.Result, nil
}

// FindHostsByIP lists A/AAAA record hostnames in the zone that point to ip.
// Used to fill the node "接入域名" from existing Cloudflare DNS.
func (c *Client) FindHostsByIP(ip string) ([]DNSRecord, error) {
	if err := c.ok(); err != nil {
		return nil, err
	}
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return nil, fmt.Errorf("无效 IP: %s", ip)
	}
	// Cloudflare allows filtering dns_records by content=
	u := fmt.Sprintf("%s/zones/%s/dns_records?content=%s&per_page=100",
		apiBase, c.ZoneID, ip)
	var out struct {
		apiResp
		Result []DNSRecord `json:"result"`
	}
	if err := c.do("GET", u, nil, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, apiErr(out.Errors)
	}
	// keep only A/AAAA
	var recs []DNSRecord
	for _, r := range out.Result {
		if r.Type == "A" || r.Type == "AAAA" {
			// normalize name (CF may return FQDN)
			r.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Name)), ".")
			recs = append(recs, r)
		}
	}
	return recs, nil
}

// VerifyToken checks token validity (optional UX).
func (c *Client) VerifyToken() error {
	if c == nil || c.Token == "" {
		return fmt.Errorf("未配置 Token")
	}
	var out apiResp
	if err := c.do("GET", apiBase+"/user/tokens/verify", nil, &out); err != nil {
		return err
	}
	if !out.Success {
		return apiErr(out.Errors)
	}
	return nil
}

// ZoneName fetches zone name for the configured zone id.
func (c *Client) ZoneName() (string, error) {
	if err := c.ok(); err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/zones/%s", apiBase, c.ZoneID)
	var out struct {
		apiResp
		Result struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := c.do("GET", u, nil, &out); err != nil {
		return "", err
	}
	if !out.Success {
		return "", apiErr(out.Errors)
	}
	return out.Result.Name, nil
}

func (c *Client) do(method, url string, body any, dest any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("cloudflare 响应解析失败 (HTTP %d): %w", resp.StatusCode, err)
	}
	return nil
}

func apiErr(errs []struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) error {
	if len(errs) == 0 {
		return fmt.Errorf("cloudflare API 失败")
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%s (%d)", e.Message, e.Code))
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}
