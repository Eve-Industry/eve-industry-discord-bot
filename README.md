# eve-industry-discord-bot

A Discord bot for the EVE Industry server, written in Go. It welcomes new
members and points them to the right place: #general, and the
feature-requests, bugs and self-hosting-support forums. Later phases add EVE
Online industry tools (market prices, blueprints, character linking).

## Status

Phase 0: the bot connects, registers slash commands and answers `/ping`.
Welcome messages, the routing panel, role picks, support posts and feedback
come next.

## Running it

1. Create an application and bot in the
   [Discord Developer Portal](https://discord.com/developers/applications).
   Under **Bot**, turn on the **Server Members Intent** (needed to see joins).
2. Invite it to your server with the `bot` and `applications.commands` scopes.
3. Copy `config.example.yaml` to `config.yaml` and fill in the server and
   channel IDs.
4. Run it with the token in the environment:

   ```sh
   export DISCORD_TOKEN=your-bot-token
   go run ./cmd/bot -config config.yaml
   ```

   Or with Docker:

   ```sh
   docker build -t eve-industry-discord-bot .
   docker run --rm -e DISCORD_TOKEN -v "$PWD:/data" eve-industry-discord-bot
   ```

Never commit `config.yaml`, `.env` or your token. `config.yaml` and `.env` are already in
`.gitignore`.

## Project layout

| Path | What it holds |
| --- | --- |
| `cmd/bot` | Entry point |
| `internal/bot` | Discord client, command sync, module wiring |
| `internal/config` | YAML config plus `DISCORD_TOKEN` |
| `internal/modules/<feature>` | One package per feature; each declares its commands and handlers |

## Development

```sh
go vet ./...
go test ./...
```

## Licence

MIT, see [LICENSE](LICENSE).
