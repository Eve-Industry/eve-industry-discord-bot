// Package market answers price questions from live Jita market data.
package market

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/rest"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/esi"
)

const (
	appraiseModalID = "/market/appraise"
	listInputID     = "items"

	maxItems    = 50 // distinct items per appraisal
	maxShown    = 10 // items listed in the appraisal reply
	parallelism = 5  // concurrent ESI requests

	marketColor = 0xF5A623
	footer      = "Jita IV - Moon 4 - Caldari Navy Assembly Plant · data from ESI, up to 5 minutes old"
)

// Market is the part of the ESI client this module needs.
type Market interface {
	ResolveType(ctx context.Context, name string) (esi.Type, error)
	ResolveTypes(ctx context.Context, names []string) (map[string]esi.Type, error)
	Orders(ctx context.Context, regionID, typeID int64) ([]esi.Order, error)
}

// Module serves /price and /appraise.
type Module struct {
	esi    Market
	logger *slog.Logger
}

// New returns the market module.
func New(m Market, logger *slog.Logger) Module {
	return Module{esi: m, logger: logger}
}

// Commands declares /price and /appraise.
func (Module) Commands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "price",
			Description: "Jita buy and sell prices for an item",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{
					Name: "item", Description: "Exact item name, e.g. Tritanium", Required: true,
				},
			},
		},
		discord.SlashCommandCreate{
			Name:        "appraise",
			Description: "Paste a list of items to value it at Jita prices",
		},
	}
}

// Register adds the market handlers.
func (m Module) Register(r handler.Router) {
	r.SlashCommand("/price", m.price)
	r.SlashCommand("/appraise", m.openAppraise)
	r.Modal(appraiseModalID, m.appraise)
}

func (m Module) price(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if err := e.DeferCreateMessage(false); err != nil {
		return err
	}
	name := data.String("item")

	t, err := m.esi.ResolveType(e.Ctx, name)
	if errors.Is(err, esi.ErrNotFound) {
		return m.reply(e.CreateFollowupMessage, fmt.Sprintf("I couldn't find an item called **%s**. Use the exact in-game name.", name))
	}
	if err != nil {
		m.logger.Error("resolve type", "name", name, "err", err)
		return m.reply(e.CreateFollowupMessage, "EVE's API isn't answering right now. Try again in a minute.")
	}
	orders, err := m.esi.Orders(e.Ctx, esi.TheForge, t.ID)
	if err != nil {
		m.logger.Error("market orders", "type", t.ID, "err", err)
		return m.reply(e.CreateFollowupMessage, "EVE's API isn't answering right now. Try again in a minute.")
	}

	_, err = e.CreateFollowupMessage(discord.MessageCreate{
		Embeds: []discord.Embed{priceEmbed(t, quoteAt(orders, esi.JitaStation))},
	})
	return err
}

func (m Module) openAppraise(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	return e.Modal(discord.NewModalCreate(appraiseModalID, "Appraise items",
		discord.NewLabel("Items", discord.NewTextInput(listInputID, discord.TextInputStyleParagraph).
			WithRequired(true).
			WithMaxLength(4000).
			WithPlaceholder("Paste from your inventory, or one item per line: Tritanium 1000")),
	))
}

func (m Module) appraise(e *handler.ModalEvent) error {
	items := parseItems(e.Data.Text(listInputID))
	if len(items) == 0 {
		return e.CreateMessage(discord.MessageCreate{Content: "I couldn't read any items from that.", Flags: discord.MessageFlagEphemeral})
	}
	if len(items) > maxItems {
		return e.CreateMessage(discord.MessageCreate{
			Content: fmt.Sprintf("That's %d different items. Please appraise %d or fewer at a time.", len(items), maxItems),
			Flags:   discord.MessageFlagEphemeral,
		})
	}
	if err := e.DeferCreateMessage(false); err != nil {
		return err
	}

	result, err := m.value(e.Ctx, items)
	if err != nil {
		m.logger.Error("appraise", "err", err)
		return m.reply(e.CreateFollowupMessage, "EVE's API isn't answering right now. Try again in a minute.")
	}
	_, err = e.CreateFollowupMessage(discord.MessageCreate{Embeds: []discord.Embed{appraisalEmbed(result)}})
	return err
}

// valued is one appraised item.
type valued struct {
	line
	typeID   int64
	typeName string
	quote    quote
}

func (v valued) sellValue() float64 { return v.quote.bestSell * float64(v.quantity) }
func (v valued) buyValue() float64  { return v.quote.bestBuy * float64(v.quantity) }

type appraisal struct {
	items   []valued
	unknown []string
}

// value resolves the names and fetches a quote for each item.
func (m Module) value(ctx context.Context, items []line) (appraisal, error) {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}
	types, err := m.esi.ResolveTypes(ctx, names)
	if err != nil {
		return appraisal{}, err
	}

	var result appraisal
	for _, it := range items {
		t, ok := types[esi.Normalize(it.name)]
		if !ok {
			result.unknown = append(result.unknown, it.name)
			continue
		}
		result.items = append(result.items, valued{line: it, typeID: t.ID, typeName: t.Name})
	}

	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, parallelism)
		mu       sync.Mutex
		firstErr error
	)
	for i := range result.items {
		wg.Add(1)
		go func(v *valued) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			orders, err := m.esi.Orders(ctx, esi.TheForge, v.typeID)
			if err != nil {
				mu.Lock()
				firstErr = cmp.Or(firstErr, err)
				mu.Unlock()
				return
			}
			v.quote = quoteAt(orders, esi.JitaStation)
		}(&result.items[i])
	}
	wg.Wait()
	if firstErr != nil {
		return appraisal{}, firstErr
	}
	return result, nil
}

func (m Module) reply(send func(discord.MessageCreate, ...rest.RequestOpt) (*discord.Message, error), content string) error {
	_, err := send(discord.MessageCreate{Content: content})
	return err
}

func priceEmbed(t esi.Type, q quote) discord.Embed {
	price := func(v float64) string {
		if v == 0 {
			return "No orders"
		}
		return formatISK(v)
	}
	return discord.NewEmbed().
		WithTitle(t.Name).
		WithThumbnail(fmt.Sprintf("https://images.evetech.net/types/%d/icon", t.ID)).
		WithColor(marketColor).
		AddField("Sell (lowest)", price(q.bestSell), true).
		AddField("Buy (highest)", price(q.bestBuy), true).
		AddField("Split", price(q.split()), true).
		AddField("Sell volume", formatCount(q.sellVolume), true).
		AddField("Buy volume", formatCount(q.buyVolume), true).
		WithFooterText(footer)
}

func appraisalEmbed(a appraisal) discord.Embed {
	var sell, buy float64
	var units int64
	for _, v := range a.items {
		sell += v.sellValue()
		buy += v.buyValue()
		units += v.quantity
	}

	top := slices.Clone(a.items)
	slices.SortStableFunc(top, func(x, y valued) int { return cmp.Compare(y.sellValue(), x.sellValue()) })
	var lines []string
	for i, v := range top {
		if i == maxShown {
			lines = append(lines, fmt.Sprintf("…and %d more", len(top)-maxShown))
			break
		}
		lines = append(lines, fmt.Sprintf("%s × %s: %s", formatCount(v.quantity), v.typeName, formatISK(v.sellValue())))
	}
	if len(a.unknown) > 0 {
		lines = append(lines, "", "Not recognised: "+strings.Join(a.unknown, ", "))
	}

	return discord.NewEmbed().
		WithTitle("Appraisal").
		WithDescription(strings.Join(lines, "\n")).
		WithColor(marketColor).
		AddField("Sell value", formatISK(sell), true).
		AddField("Buy value", formatISK(buy), true).
		AddField("Items", fmt.Sprintf("%d types, %s units", len(a.items), formatCount(units)), true).
		WithFooterText(footer)
}
