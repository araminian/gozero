package metric

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type mockStore struct{}

func (m *mockStore) GetAllScaleUpKeysValues() (map[string]string, error) {
	return map[string]string{"bar-foo-svc-cluster-local": "10"}, nil
}

func TestFiberMetricExposer(t *testing.T) {
	exposer, err := NewFiberMetricExposer(WithFiberMetricExposerPath("/metrics"), WithFiberMetricExposerPort(8080))
	if err != nil {
		t.Fatalf("failed to create exposer: %v", err)
	}

	c, cancel := context.WithCancel(context.Background())
	go exposer.Start(c, &mockStore{})
	defer cancel()
	defer exposer.Shutdown(c)

	// Ask for existing host metrics
	req, err := http.NewRequestWithContext(c, "GET", "http://localhost:8080/metrics/bar-foo-svc-cluster-local", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	t.Logf("response body: %s", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// response should be a json object with a single key which is {"value": "10"}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if result["value"] != "10" {
		t.Fatalf("expected value %s, got %s", "10", result["value"])
	}

	// Ask for non-existing host metrics
	req, err = http.NewRequestWithContext(c, "GET", "http://localhost:8080/metrics/no-foo-svc-cluster-local", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}

	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	t.Logf("response body: %s", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// response should be a json object with a single key which is {"value": "0"}
	var resultNotFound map[string]string
	if err := json.Unmarshal(body, &resultNotFound); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if resultNotFound["value"] != "0" {
		t.Fatalf("expected value %s, got %s", "0", resultNotFound["value"])
	}

}
