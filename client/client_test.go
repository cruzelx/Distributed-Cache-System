package cache_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	cache "distributed-cache/client"
)

// newTestServer sets up a minimal fake cache server and returns a client
// pointed at it along with a teardown function.
func newTestServer(mux *http.ServeMux) (*cache.Client, func()) {
	srv := httptest.NewServer(mux)
	c := cache.New(srv.Listener.Addr().String())
	return c, srv.Close
}

func TestSet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	if err := c.Set(context.Background(), "hello", "world"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
}

func TestGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key":"hello","value":"world"}`))
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	val, err := c.Get(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if val != "world" {
		t.Fatalf("Get: got %q, want %q", val, "world")
	}
}

func TestGet_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "key missing not found", http.StatusNotFound)
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	_, err := c.Get(context.Background(), "missing")
	if !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("Get missing key: want ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/hello", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
		}
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	if err := c.Delete(context.Background(), "hello"); err != nil {
		t.Fatalf("Delete: unexpected error: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/ghost", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "key ghost not found", http.StatusNotFound)
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	err := c.Delete(context.Background(), "ghost")
	if !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("Delete missing key: want ErrNotFound, got %v", err)
	}
}

func TestBulkSet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/bulk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	err := c.BulkSet(context.Background(), map[string]string{
		"a": "1",
		"b": "2",
	})
	if err != nil {
		t.Fatalf("BulkSet: unexpected error: %v", err)
	}
}

func TestBulkGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data/bulk/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"a":"1","b":"2"}`))
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	result, err := c.BulkGet(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("BulkGet: unexpected error: %v", err)
	}
	if result["a"] != "1" || result["b"] != "2" {
		t.Fatalf("BulkGet: unexpected result: %v", result)
	}
}

func TestHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	c, teardown := newTestServer(mux)
	defer teardown()

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: unexpected error: %v", err)
	}
}
