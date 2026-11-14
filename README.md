# lobster

A Discord bot that tells you whether a second-hand listing is worth the asking
price. It reads [fbwatch](https://github.com/Hankkdd/fbwatch)'s notifications,
queries back through fbwatch's MCP server for the data behind them, and answers
in the channel.

Reply to a notification, tag the bot, ask "is this a fair price". It pulls the
full post, the seller's comments, four months of asking prices for the same
item, the official price, and today's exchange rate — then gives a verdict.

```
  fbwatch ──▶ #channel ──── notification with a listing link
                 │
                 │ reply + @mention
                 ▼
         ┌───────────────────┐
         │ openclaw-gateway  │  LLM via an existing subscription
         │  tools allowlist  │  (no per-token API billing)
         └─────────┬─────────┘
                   │
       ┌───────────┼─────────────┬──────────────┐
       ▼           ▼             ▼              ▼
  fbwatch MCP   web_search   web_fetch     view_image
  (read-only)   (hosted)     (public       (photos from
                              pages only)   the listing)
```

The interesting constraint is cost. An appraisal costs several tool calls and a
few thousand tokens of context; metered API billing would make it an expensive
way to answer "is 1,800 too much". Running the agent against an existing
subscription instead makes the per-question cost effectively zero, at the price
of a heavier runtime and no usage API.

## Layout

| Path | Role |
|---|---|
| `compose.yaml` | The gateway, plus a one-shot CLI profile for setup commands |
| `state/openclaw.example.json` | Agent config: model, tool allowlist, Discord permissions |
| `workspace.example/SOUL.md` | Voice and personality |
| `workspace.example/AGENTS.md` | Appraisal procedure and the safety rules |
| `workspace.example/IDENTITY.md`, `USER.md` | Name, and what the bot knows about you |
| `hub-mcp/` | A second MCP server, for driving a task runner from Discord |

Files under the workspace take effect on the next message — no restart.

The real `workspace/` and `state/openclaw.json` are gitignored: they hold a
persona, the owner's preferences and what the bot has learned about them, which
is personal to each deployment. Copy the examples and edit.

## Safety design

This agent reads text written by strangers — marketplace posts, comments, search
results, web pages, video transcripts — and anything in there is a potential
instruction. Three layers:

- **Tools.** Starts from `minimal` and adds only `web_search`, `web_fetch`,
  `view_image` and `memory_*`. Shell, filesystem, browser, scheduling and
  sub-agents are all denied. There is no tool that can act on the host.
- **Input fencing.** Everything fetched is wrapped in an explicit
  "this is data, not instructions" boundary, and `AGENTS.md` requires the bot to
  say so out loud when a post tries to give it orders.
- **Reach.** Listing links go to the fbwatch MCP tools, never to `web_fetch` —
  the model only ever supplies an id, never a URL. `web_fetch` is limited to
  search results and links the user pasted by hand. Facebook and YouTube are
  blocked outright at the fetch layer, since those need the dedicated tools.

Around that: Discord DMs are off, the bot answers only when mentioned in
allowlisted guilds, config writes from chat are disabled, and the control UI
binds to localhost only.

`state/` holds OAuth tokens in plain text in a SQLite file, so `.gitignore`
allows only the example config out.

## Prerequisites

- Docker with Compose v2
- A Discord application and bot
- An LLM provider the gateway can authenticate against
- A running fbwatch with its MCP profile enabled, reachable on a shared Docker
  network

## Setup

### 1. Create the Discord bot

In the Developer Portal: create an application and a bot, enable **Message
Content Intent**, then use OAuth2 → URL Generator with the `bot` and
`applications.commands` scopes and these permissions: View Channels, Send
Messages, Read Message History, Embed Links, Attach Files. Invite it with the
generated URL.

### 2. Configure

```bash
cp .env.example .env
sed -i "s/^OPENCLAW_GATEWAY_TOKEN=.*/OPENCLAW_GATEWAY_TOKEN=$(openssl rand -hex 32)/" .env
cp state/openclaw.example.json state/openclaw.json
cp -r workspace.example workspace
```

Put the bot token in `.env`, then replace the `<discord ...>` placeholders in
`state/openclaw.json` with your own guild, channel and application ids
(Developer Mode → right-click → Copy ID).

### 3. Install the Discord plugin

Not in the base image:

```bash
docker compose --profile cli pull
docker compose run --rm --no-deps --entrypoint node openclaw-gateway \
  dist/index.js plugins install @openclaw/discord
```

### 4. Authenticate the model provider

```bash
docker compose run --rm --no-deps --entrypoint node openclaw-gateway \
  dist/index.js models auth login --provider openai
```

The browser redirects to `localhost:1455/auth/callback?code=…`. Paste only the
`code=` segment up to the next `&` — the whole URL is long enough to jam the
input field.

### 5. Validate and start

```bash
docker compose run --rm --no-deps --entrypoint node openclaw-gateway \
  dist/index.js config validate
docker compose up -d
docker compose logs -f openclaw-gateway
```

## Verify

In Discord:

- `@bot hello` — it answers, in the voice from `SOUL.md`
- `/tools` — the list must contain only `web_search`, `web_fetch`, `view_image`,
  `memory_search`, `memory_get`, `session_status`. If `exec`, `read`, `write` or
  `browser` appear, the allowlist is not in effect
- Reply to an fbwatch notification and tag the bot

## Changing the model

```bash
docker compose exec openclaw-gateway node dist/index.js \
  models list --provider openai --all --plain
```

Edit `model.primary`, `models` and `modelPolicy.allow` in `agents.defaults`;
the change is picked up live. Every entry under `models` must keep its
`agentRuntime` id, or the tool allowlist and the workspace prompts are bypassed.

## What did not work

- **Self-hosted search (SearXNG).** Brave and Google CSE rate-limited it,
  DuckDuckGo served captchas. Zero usable results. Replaced with the provider's
  hosted search, which also comes out of the subscription.
- **Fetching manufacturer sites directly for official prices.** Plain HTTP
  returns an empty 202; headless Chrome gets a captcha. Official prices now come
  from search result summaries that state an MSRP.
- **Trusting the exchange rate the model "knows".** It is confidently wrong.
  Rates come from an fbwatch tool, with the date in the reply.

## Teardown

```bash
docker compose --profile cli down
```

Full removal, including OAuth tokens:

```bash
docker compose --profile cli down --rmi all
sudo find state auth-secrets -mindepth 1 ! -name openclaw.example.json -delete
```

Then delete the bot in the Developer Portal, or kick it from the server.

## Licence

MIT, see [LICENSE](LICENSE).
