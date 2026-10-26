package market

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/esi"
)

type fakeMarket struct {
	types  map[string]esi.Type
	orders map[int64][]esi.Order
}

func (f fakeMarket) ResolveType(_ context.Context, name string) (esi.Type, error) {
	t, ok := f.types[esi.Normalize(name)]
	if !ok {
		return esi.Type{}, esi.ErrNotFound
	}
	return t, nil
}

func (f fakeMarket) ResolveTypes(_ context.Context, names []string) (map[string]esi.Type, error) {
	out := map[string]esi.Type{}
	for _, n := range names {
		if t, ok := f.types[esi.Normalize(n)]; ok {
			out[esi.Normalize(n)] = t
		}
	}
	return out, nil
}

func (f fakeMarket) Orders(_ context.Context, _ int64, typeID int64) ([]esi.Order, error) {
	return f.orders[typeID], nil
}

func TestValue(t *testing.T) {
	jita := esi.JitaStation
	m := New(fakeMarket{
		types: map[string]esi.Type{
			"tritanium": {ID: 34, Name: "Tritanium"},
			"pyerite":   {ID: 35, Name: "Pyerite"},
		},
		orders: map[int64][]esi.Order{
			34: {{LocationID: jita, Price: 5, VolumeRemain: 1}, {LocationID: jita, IsBuyOrder: true, Price: 4, VolumeRemain: 1}},
			35: {{LocationID: jita, Price: 10, VolumeRemain: 1}},
		},
	}, slog.Default())

	a, err := m.value(context.Background(), parseItems("Tritanium 100\npyerite 10\nNot A Thing 5"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.items) != 2 || len(a.unknown) != 1 || a.unknown[0] != "Not A Thing" {
		t.Fatalf("appraisal = %+v", a)
	}

	embed := appraisalEmbed(a)
	fields := map[string]string{}
	for _, f := range embed.Fields {
		fields[f.Name] = f.Value
	}
	// Sell: 100×5 + 10×10 = 600. Buy: 100×4 + 10×0 = 400.
	if fields["Sell value"] != "600.00 ISK" || fields["Buy value"] != "400.00 ISK" {
		t.Errorf("fields = %v", fields)
	}
	// Highest value first: Tritanium (500) before Pyerite (100).
	if !strings.HasPrefix(embed.Description, "100 × Tritanium") {
		t.Errorf("description = %q", embed.Description)
	}
	if !strings.Contains(embed.Description, "Not recognised: Not A Thing") {
		t.Errorf("description doesn't list the unknown item: %q", embed.Description)
	}
}

func TestPriceEmbedNoOrders(t *testing.T) {
	e := priceEmbed(esi.Type{ID: 34, Name: "Tritanium"}, quote{})
	if e.Fields[0].Value != "No orders" {
		t.Errorf("sell field = %q, want No orders", e.Fields[0].Value)
	}
}
