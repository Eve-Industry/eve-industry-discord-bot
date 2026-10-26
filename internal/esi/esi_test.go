package esi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/universe/ids/" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua != "test-agent" {
			t.Errorf("User-Agent = %q", ua)
		}
		var names []string
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &names)
		if len(names) == 1 && strings.EqualFold(names[0], "tritanium") {
			fmt.Fprint(w, `{"inventory_types":[{"id":34,"name":"Tritanium"}]}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "test-agent")

	got, err := c.ResolveType(context.Background(), "  tritanium ")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 34 || got.Name != "Tritanium" {
		t.Errorf("got %+v", got)
	}

	if _, err := c.ResolveType(context.Background(), "Not An Item"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestOrdersPagesAndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("type_id") != "34" {
			t.Errorf("type_id = %q", r.URL.Query().Get("type_id"))
		}
		w.Header().Set("X-Pages", "2")
		w.Header().Set("Expires", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		fmt.Fprintf(w, `[{"order_id":%s,"type_id":34,"location_id":60003760,"is_buy_order":false,"price":5.5,"volume_remain":100}]`,
			r.URL.Query().Get("page"))
	}))
	defer srv.Close()
	c := New(srv.URL, "test-agent")

	orders, err := c.Orders(context.Background(), TheForge, 34)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 2 || orders[0].OrderID != 1 || orders[1].OrderID != 2 {
		t.Errorf("orders = %+v", orders)
	}

	if _, err := c.Orders(context.Background(), TheForge, 34); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("made %d requests, want 2 (second call should be cached)", n)
	}
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "test-agent").Orders(context.Background(), TheForge, 34)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want a 503 error", err)
	}
}
