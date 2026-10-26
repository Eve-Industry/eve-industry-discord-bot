package market

import (
	"bufio"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/esi"
)

// quote is the best prices and depth for one item at one station.
type quote struct {
	bestSell   float64 // lowest sell order, 0 if none
	bestBuy    float64 // highest buy order, 0 if none
	sellVolume int64
	buyVolume  int64
}

// split is the midpoint between the best buy and sell, or whichever exists.
func (q quote) split() float64 {
	switch {
	case q.bestSell > 0 && q.bestBuy > 0:
		return (q.bestSell + q.bestBuy) / 2
	case q.bestSell > 0:
		return q.bestSell
	default:
		return q.bestBuy
	}
}

// quoteAt summarises the orders placed at station.
func quoteAt(orders []esi.Order, station int64) quote {
	var q quote
	for _, o := range orders {
		if o.LocationID != station {
			continue
		}
		if o.IsBuyOrder {
			q.buyVolume += o.VolumeRemain
			if o.Price > q.bestBuy {
				q.bestBuy = o.Price
			}
			continue
		}
		q.sellVolume += o.VolumeRemain
		if q.bestSell == 0 || o.Price < q.bestSell {
			q.bestSell = o.Price
		}
	}
	return q
}

// line is one item from a pasted list.
type line struct {
	name     string
	quantity int64
}

// parseItems reads a pasted list. It understands the tab-separated text EVE
// copies from inventories and contracts ("Tritanium<TAB>1,000<TAB>..."), and
// hand-typed lines like "Tritanium 1000", "Tritanium x1000" or "Tritanium".
// Repeated items are added together, keeping the order they first appear in.
func parseItems(text string) []line {
	var out []line
	index := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		name, qty := parseLine(sc.Text())
		if name == "" || qty <= 0 {
			continue
		}
		key := strings.ToLower(name)
		if i, ok := index[key]; ok {
			out[i].quantity += qty
			continue
		}
		index[key] = len(out)
		out = append(out, line{name: name, quantity: qty})
	}
	return out
}

func parseLine(s string) (string, int64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	if strings.Contains(s, "\t") {
		fields := strings.Split(s, "\t")
		name := strings.TrimSpace(fields[0])
		if len(fields) > 1 {
			if q, ok := parseQuantity(fields[1]); ok {
				return name, q
			}
		}
		return name, 1
	}
	words := strings.Fields(s)
	if len(words) > 1 {
		if q, ok := parseQuantity(strings.TrimPrefix(strings.ToLower(words[len(words)-1]), "x")); ok {
			return strings.Join(words[:len(words)-1], " "), q
		}
	}
	return strings.Join(words, " "), 1
}

// parseQuantity reads whole numbers written with thousands separators,
// e.g. "1,000", "1.000" or "1 000".
func parseQuantity(s string) (int64, bool) {
	s = strings.NewReplacer(",", "", ".", "", " ", "", " ", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// formatISK prints an amount like "1,234,567.89 ISK".
func formatISK(v float64) string {
	cents := int64(math.Round(v * 100))
	whole, frac := cents/100, cents%100
	digits := strconv.FormatInt(whole, 10)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return fmt.Sprintf("%s.%02d ISK", b.String(), frac)
}

// formatCount prints a whole number with thousands separators.
func formatCount(n int64) string {
	s := formatISK(float64(n))
	return strings.TrimSuffix(s, ".00 ISK")
}
