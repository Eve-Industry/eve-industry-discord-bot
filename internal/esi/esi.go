// Package esi is a small client for the public parts of EVE Online's ESI
// API. It only covers what the bot needs and caches responses for as long
// as ESI says they stay fresh.
package esi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Well-known IDs.
const (
	TheForge     int64 = 10000002 // region containing Jita
	JitaStation  int64 = 60003760 // Jita IV - Moon 4 - Caldari Navy Assembly Plant
	DefaultURL         = "https://esi.evetech.net/latest"
	maxPages           = 20
	defaultCache       = 5 * time.Minute
)

// ErrNotFound is returned when ESI knows no item by that name.
var ErrNotFound = errors.New("not found")

// Client calls ESI.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body    []byte
	pages   int
	expires time.Time
}

// New returns a client. userAgent should say who runs the bot and how to
// reach them, as ESI asks.
func New(baseURL, userAgent string) *Client {
	return &Client{
		baseURL:   baseURL,
		userAgent: userAgent,
		http:      &http.Client{Timeout: 15 * time.Second},
		now:       time.Now,
		cache:     map[string]cached{},
	}
}

// Type is an item type.
type Type struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Order is one market order.
type Order struct {
	OrderID      int64   `json:"order_id"`
	TypeID       int64   `json:"type_id"`
	LocationID   int64   `json:"location_id"`
	IsBuyOrder   bool    `json:"is_buy_order"`
	Price        float64 `json:"price"`
	VolumeRemain int64   `json:"volume_remain"`
}

// ResolveTypes looks up item types by exact name (ESI ignores case). Names
// ESI doesn't recognise are left out of the result.
func (c *Client) ResolveTypes(ctx context.Context, names []string) (map[string]Type, error) {
	clean := make([]string, len(names))
	for i, n := range names {
		clean[i] = strings.Join(strings.Fields(n), " ")
	}
	body, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/universe/ids/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		InventoryTypes []Type `json:"inventory_types"`
	}
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, fmt.Errorf("decode ids: %w", err)
	}
	types := make(map[string]Type, len(out.InventoryTypes))
	for _, t := range out.InventoryTypes {
		types[Normalize(t.Name)] = t
	}
	return types, nil
}

// ResolveType looks up one item type by exact name.
func (c *Client) ResolveType(ctx context.Context, name string) (Type, error) {
	types, err := c.ResolveTypes(ctx, []string{name})
	if err != nil {
		return Type{}, err
	}
	t, ok := types[Normalize(name)]
	if !ok {
		return Type{}, ErrNotFound
	}
	return t, nil
}

// Orders returns every open order for typeID in regionID.
func (c *Client) Orders(ctx context.Context, regionID, typeID int64) ([]Order, error) {
	var all []Order
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("%s/markets/%d/orders/?order_type=all&type_id=%d&page=%d", c.baseURL, regionID, typeID, page)
		res, err := c.get(ctx, url)
		if err != nil {
			return nil, err
		}
		var orders []Order
		if err := json.Unmarshal(res.body, &orders); err != nil {
			return nil, fmt.Errorf("decode orders: %w", err)
		}
		all = append(all, orders...)
		if page >= res.pages {
			break
		}
	}
	return all, nil
}

func (c *Client) get(ctx context.Context, url string) (cached, error) {
	c.mu.Lock()
	hit, ok := c.cache[url]
	c.mu.Unlock()
	if ok && c.now().Before(hit.expires) {
		return hit, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return cached{}, err
	}
	res, err := c.do(req)
	if err != nil {
		return cached{}, err
	}
	c.mu.Lock()
	c.cache[url] = res
	c.mu.Unlock()
	return res, nil
}

func (c *Client) do(req *http.Request) (cached, error) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return cached{}, fmt.Errorf("esi: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return cached{}, fmt.Errorf("esi: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cached{}, fmt.Errorf("esi: %s %s: %s", req.Method, req.URL.Path, resp.Status)
	}

	res := cached{body: body, pages: 1, expires: c.now().Add(defaultCache)}
	if p, err := strconv.Atoi(resp.Header.Get("X-Pages")); err == nil && p > 0 {
		res.pages = p
	}
	if t, err := http.ParseTime(resp.Header.Get("Expires")); err == nil {
		res.expires = t
	}
	return res, nil
}
