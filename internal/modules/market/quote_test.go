package market

import (
	"math"
	"reflect"
	"testing"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/esi"
)

func TestQuoteAt(t *testing.T) {
	orders := []esi.Order{
		{LocationID: esi.JitaStation, Price: 5.10, VolumeRemain: 100},
		{LocationID: esi.JitaStation, Price: 4.90, VolumeRemain: 50},
		{LocationID: esi.JitaStation, IsBuyOrder: true, Price: 4.50, VolumeRemain: 1000},
		{LocationID: esi.JitaStation, IsBuyOrder: true, Price: 4.70, VolumeRemain: 10},
		{LocationID: 1, Price: 1.00, VolumeRemain: 9999}, // elsewhere: ignored
	}
	q := quoteAt(orders, esi.JitaStation)
	want := quote{bestSell: 4.90, bestBuy: 4.70, sellVolume: 150, buyVolume: 1010}
	if q != want {
		t.Errorf("quote = %+v, want %+v", q, want)
	}
	if got := q.split(); math.Abs(got-4.80) > 1e-9 {
		t.Errorf("split = %v, want 4.80", got)
	}
	if got := (quote{bestBuy: 3}).split(); got != 3 {
		t.Errorf("split with only buys = %v, want 3", got)
	}
}

func TestParseItems(t *testing.T) {
	text := "Tritanium\t1,000\tMineral\t\t10 m3\n" +
		"Pyerite 500\n" +
		"Rifter x2\n" +
		"Damage Control II\n" +
		"tritanium\t250\n" +
		"\n"
	want := []line{
		{"Tritanium", 1250},
		{"Pyerite", 500},
		{"Rifter", 2},
		{"Damage Control II", 1},
	}
	if got := parseItems(text); !reflect.DeepEqual(got, want) {
		t.Errorf("parseItems =\n%+v\nwant\n%+v", got, want)
	}
}

func TestFormatISK(t *testing.T) {
	tests := map[float64]string{
		0:           "0.00 ISK",
		4.8:         "4.80 ISK",
		1234567.891: "1,234,567.89 ISK",
		999.999:     "1,000.00 ISK",
	}
	for in, want := range tests {
		if got := formatISK(in); got != want {
			t.Errorf("formatISK(%v) = %q, want %q", in, got, want)
		}
	}
	if got := formatCount(1250000); got != "1,250,000" {
		t.Errorf("formatCount = %q", got)
	}
}
