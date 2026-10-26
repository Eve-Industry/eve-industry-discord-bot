# eve-industry-discord-bot

A Discord bot for the EVE Industry server, written in Go. It welcomes new
members and points them to the right place: #general, and the
feature-requests, bugs and self-hosting-support forums. Later phases add EVE
Online industry tools (market prices, blueprints, character linking).

## Features

- **Welcome:** greets each new member in #general with a **Get started**
  button that shows the routing panel just to them. With the rules gate on,
  the welcome is posted when they accept the rules instead.
- **Rules gate (optional):** `/setup rules` posts an **Accept** button.
  Pressing it gives the `roles.member` role, which should be the role that
  can see the rest of the server.
- **Routing panel:** `/setup panel` (needs Manage Server) posts a "Where to
  go" message with buttons to #general and self-hosting-support, plus
  **Request a feature** and **Report a bug** forms.
- **Feature and bug forms:** short forms that post a tidy, consistent thread
  in the feature-requests or bugs forum, mentioning who sent it. Staff mark
  status with the forum's own tags.
- **Pick roles:** a menu of the roles listed under `roles.self_assign`.
  Members tick and untick roles; other roles are never touched.
- **Contact staff:** a form that opens a private thread only the member and
  the staff role can see, with **Claim** and **Close** buttons.
- **FAQ:** `/faq topic:<name>` posts a saved answer, optionally mentioning
  someone. Staff add, edit and remove answers with `/faq-admin set` and
  `/faq-admin delete` (needs Manage Server). Answers live in the SQLite file
  set by `database`.
- **Mod log (optional):** joins (flagging accounts under a week old),
  leaves and role changes are posted in `channels.mod_log`.
- **Prices:** `/price item:<name>` shows the best Jita 4-4 sell and buy
  prices, the split and order volumes. `/appraise` opens a box to paste an
  inventory or a list like `Tritanium 1000`, and replies with the total sell
  and buy value at Jita. Data comes from EVE's ESI API and is cached for as
  long as ESI says (about 5 minutes). Set `esi.contact` so CCP can reach you.
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
   docker run --rm --user "$(id -u):$(id -g)" -e DISCORD_TOKEN -v "$PWD:/data" eve-industry-discord-bot
   ```

5. Run `/setup panel` in #general and pin the message it posts. If you use
   the rules gate, run `/setup rules` in your rules channel under the rules.

The first start copies the channels, roles and welcome text from
`config.yaml` into the database. Until an admin changes them in the admin
dashboard (coming soon), edits to `config.yaml` are picked up on the next
restart. After that the database copy wins and the bot logs a warning if the
file differs.

Never commit `config.yaml`, `.env` or your token. `config.yaml` and `.env` are already in
`.gitignore`.

## Project layout

| Path | What it holds |
| --- | --- |
| `cmd/bot` | Entry point |
| `internal/bot` | Discord client, command sync, module wiring |
| `internal/config` | YAML config plus `DISCORD_TOKEN` |
| `internal/settings` | Live channels, roles and welcome text, stored in the database and changeable while the bot runs |
| `internal/modules/<feature>` | One package per feature; each declares its commands and handlers |
| `internal/esi` | Small cached client for EVE's public ESI API |
| `internal/store` | SQLite database (pure Go, no CGO): FAQ answers, settings, audit log |

## Development

```sh
go vet ./...
go test ./...
```

## Licence

MIT, see [LICENSE](LICENSE).
