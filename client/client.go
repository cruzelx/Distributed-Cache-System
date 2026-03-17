// Package cache provides a Go client for the distributed cache system.
// It communicates with the master node (typically via the nginx load balancer)
// and exposes Set, Get, Delete, BulkSet, and BulkGet operations.
package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is returned by Get and Delete when the key does not exist.
var ErrNotFound = errors.New("key not found")

// Client is a client for the distributed cache. It is safe for concurrent use.
type Client struct {
	baseURL string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the HTTP timeout for every request (default: 5s).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

// WithHTTPClient replaces the underlying HTTP client entirely.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.http = hc
	}
}

// New creates a Client that sends requests to addr (e.g. "localhost:8080").
// addr should be the address of the nginx load balancer or primary master.
func New(addr string, opts ...Option) *Client {
	c := &Client{
		baseURL: "http://" + addr,
		http: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 64,
			},
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Set stores key with value in the cache. It overwrites any existing value.
func (c *Client) Set(ctx context.Context, key, value string) error {
	body, err := json.Marshal(struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{key, value})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/data", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set %q: server returned %s", key, resp.Status)
	}
	return nil
}

// Get retrieves the value for key. Returns ErrNotFound if the key does not exist.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/data/"+key, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %q: server returned %s", key, resp.Status)
	}
	var result struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("get %q: decode response: %w", key, err)
	}
	return result.Value, nil
}

// Delete removes key from the cache. Returns ErrNotFound if the key does not exist.
func (c *Client) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/data/"+key, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete %q: server returned %s", key, resp.Status)
	}
	return nil
}

// BulkSet stores all key-value pairs in a single request.
func (c *Client) BulkSet(ctx context.Context, entries map[string]string) error {
	type kv struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	pairs := make([]kv, 0, len(entries))
	for k, v := range entries {
		pairs = append(pairs, kv{k, v})
	}
	body, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/data/bulk", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bulk set: server returned %s", resp.Status)
	}
	return nil
}

// BulkGet retrieves values for the given keys in a single request.
// Keys that do not exist are simply absent from the returned map.
func (c *Client) BulkGet(ctx context.Context, keys []string) (map[string]string, error) {
	body, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/data/bulk/get", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bulk get: server returned %s: %s", resp.Status, b)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("bulk get: decode response: %w", err)
	}
	return result, nil
}

// Health returns nil if the master is reachable and healthy.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: server returned %s", resp.Status)
	}
	return nil
}
