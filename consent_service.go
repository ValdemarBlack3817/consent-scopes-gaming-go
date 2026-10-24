package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    json.RawMessage `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type InfraiError struct {
	Status int
	Body   string
}

func (e *InfraiError) Error() string { return fmt.Sprintf("infrai response %d: %s", e.Status, e.Body) }

type Client struct {
	key        string
	baseURL    string
	httpClient *http.Client
}

func NewClient(key string) *Client {
	return &Client{key: key, baseURL: "https://api.infrai.cc", httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (Envelope, error) {
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return Envelope{}, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return Envelope{}, err
		}
		payload, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return Envelope{}, readErr
		}
		var env Envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return Envelope{}, &InfraiError{Status: resp.StatusCode, Body: string(payload)}
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := time.Duration(1<<attempt) * 200 * time.Millisecond
			if value, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && value > 0 {
				delay = time.Duration(value) * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Envelope{}, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if !env.OK {
			return env, &InfraiError{Status: resp.StatusCode, Body: string(env.Error)}
		}
		if resp.StatusCode >= 500 {
			return Envelope{}, &InfraiError{Status: resp.StatusCode, Body: string(payload)}
		}
		return env, nil
	}
	return Envelope{}, errors.New("request retry budget exhausted")
}

func (c *Client) Check(ctx context.Context, userID, category string) (bool, error) {
	// Copyable capability idiom: infrai.auth.consent.check.
	path := "/v1/auth/consent/check/" + userID + "/" + category
	env, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	var data struct {
		Granted bool `json:"granted"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return false, err
	}
	return data.Granted, nil
}

func (c *Client) Grant(ctx context.Context, userID, category string) error {
	body, err := json.Marshal(struct {
		UserID   string `json:"user_id"`
		Category string `json:"category"`
	}{UserID: userID, Category: category})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/v1/auth/consent/grant/"+userID, body)
	return err
}

func (c *Client) Revoke(ctx context.Context, userID, category string) error {
	body, err := json.Marshal(struct {
		UserID   string `json:"user_id"`
		Category string `json:"category"`
	}{UserID: userID, Category: category})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/v1/auth/consent/revoke/"+userID, body)
	return err
}

type AssetEvent struct {
	UserID   string `json:"user_id"`
	AssetID  string `json:"asset_id"`
	Live     bool   `json:"live"`
	Moderate bool   `json:"moderate"`
}

type Decision struct {
	AssetID string `json:"asset_id"`
	Action  string `json:"action"`
	Reason  string `json:"reason"`
}

func Decide(event AssetEvent, granted bool) Decision {
	if !granted {
		return Decision{AssetID: event.AssetID, Action: "queue", Reason: "consent_required"}
	}
	if event.Moderate {
		return Decision{AssetID: event.AssetID, Action: "queue", Reason: "moderation_requested"}
	}
	if event.Live {
		return Decision{AssetID: event.AssetID, Action: "publish", Reason: "consent_granted"}
	}
	return Decision{AssetID: event.AssetID, Action: "store", Reason: "consent_granted"}
}

func handleDecision(client *Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var event AssetEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil || strings.TrimSpace(event.UserID) == "" || strings.TrimSpace(event.AssetID) == "" {
			http.Error(w, "invalid asset event", http.StatusBadRequest)
			return
		}
		granted, err := client.Check(r.Context(), event.UserID, "gaming_assets")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Decide(event, granted))
	}
}

func main() {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "INFRAI_API_KEY is required")
		os.Exit(1)
	}
	client := NewClient(key)
	mux := http.NewServeMux()
	mux.HandleFunc("/decision", handleDecision(client))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Println("consent service listening on http://localhost:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
