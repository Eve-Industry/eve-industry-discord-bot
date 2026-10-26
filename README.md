# eve-industry-discord-bot

A Discord bot for the EVE Industry server, written in Go. It welcomes new
members and points them to the right place: #general, and the
feature-requests, bugs and self-hosting-support forums. Later phases add EVE
Online industry tools (market prices, blueprints, character linking).

## Features

- **Welcome:** greets each new member in #general with a **Get started**
  button that shows the routing panel just to them.
- **Routing panel:** `/setup panel` (needs Manage Server) posts a "Where to
  go" message with buttons to #general, self-hosting-support,
  feature-requests and bugs.
- **Pick roles:** a menu of the roles listed under `roles.self_assign`.
  Members tick and untick roles; other roles are never touched.
- **Contact staff:** a form that opens a private thread only the member and
  the staff role can see, with **Claim** and **Close** buttons.
- `/ping` checks the bot is online.

Panel buttons for anything not configured are hidden.

## Running it

1. Create an application and bot in the
   [Discord Developer Portal](https://discord.com/developers/applications).
   Under **Bot**, turn on the **Server Members Intent** (needed to see joins).
2. Invite it to your server with the `bot` and `applications.commands`
   scopes and these permissions: View Channels, Send Messages, Embed Links,
   Manage Roles, Create Private Threads, Send Messages in Threads and Manage
   Threads. In Server Settings > Roles, drag the bot's role **above** every
   role in `roles.self_assign`, or it can't hand them out.
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

5. Run `/setup panel` in #general and pin the message it posts.

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
