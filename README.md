<h1 align="center">MyAppVPN-subs</h1>

<p align="center"><b>Auto-collected VLESS subscription: a bot finds, checks and cleans public servers every few hours.</b></p>

<p align="center">
  <b>English</b> · <a href="README.ru.md">Русский</a>
</p>

<p align="center">
  <a href="https://github.com/indelingDanil/MyAppVPN-subs/tree/output"><img src="https://img.shields.io/github/last-commit/indelingDanil/MyAppVPN-subs/output?label=list%20updated" alt="List last updated"></a>
  <a href="https://github.com/indelingDanil/MyAppVPN-subs/actions/workflows/collect.yml"><img src="https://img.shields.io/github/actions/workflow/status/indelingDanil/MyAppVPN-subs/collect.yml?branch=main&label=collect" alt="collect workflow status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="License: MIT"></a>
  <a href="https://t.me/myappvpn_dprog"><img src="https://img.shields.io/badge/Telegram-channel-26A5E4?logo=telegram&logoColor=white" alt="Telegram channel"></a>
</p>

## Subscription links

Use **jsDelivr** first: `raw.githubusercontent.com` is often slow or blocked in Russia.

| File | jsDelivr (recommended) | githack | GitHub raw |
|---|---|---|---|
| `vless.txt` (main list) | [link](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt) | [link](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/vless.txt) | [link](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/vless.txt) |
| `reality.txt` (REALITY only) | [link](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/reality.txt) | [link](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/reality.txt) | [link](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/reality.txt) |
| `by-country/<CC>.txt`, e.g. `DE` | [link](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/by-country/DE.txt) | [link](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/by-country/DE.txt) | [link](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/by-country/DE.txt) |
| `all.txt` (every alive server) | [link](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/all.txt) | [link](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/all.txt) | [link](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/all.txt) |

The main link for v2rayNG, Hiddify and similar clients:

```
https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt
```

**[MyAppVPN](https://github.com/indelingDanil/MyAppVPN)** already has it built in as "MyAppVPN (авто-сбор)" (auto-collected). You can turn it off in Settings → Subscriptions.

### What's in the `output` branch

| File | Contents |
|---|---|
| `vless.txt` / `vless.b64` | Main list: up to 500 servers, REALITY first, then TLS, at most 40 per country, no RU/UA exits |
| `reality.txt` / `reality.b64` | REALITY only: up to 500 servers, at most 60 per country, no RU/UA exits |
| `by-country/<CC>.txt` | One file per country (ISO code), up to 500 servers. `RU.txt` exists for whitelist-only networks; `UA` is not published |
| `all.txt` / `all.b64` | Every server that passed the check, in the same order, no line or per-country limit, RU included, no UA. Can be several MB: meant for clients that handle big lists, not for the app |
| `meta.json` | Update time, counts (collected / TCP-alive / xray-alive / published, total / reality / tls / per country), sources used, excluded sources with reasons |
| `stats.md` | The same stats in readable form |

Each file has one `vless://` link per line (the `.b64` files hold the same list in base64). All files except `all.txt` stay under 1 MB. If `all.txt` ever outgrows GitHub's 100 MB file limit, it is split into `all-1.txt`, `all-2.txt`… (listed in `meta.json`); jsDelivr does not serve files over 20 MB, so for a list that big use the githack or GitHub raw link. Server names are rewritten as `🇩🇪 DE reality-0012`, and MyAppVPN reads the country from the flag. Each run replaces the branch with a single commit, and the file paths stay the same.

## How it works

```
discover (weekly) → collect → validate → publish   (every few hours)
```

1. **Discover.** The bot searches GitHub for repos with public VLESS subscriptions pushed in the last 7 days and pulls raw file links out of them. It accepts a source with at least 20 valid `vless://` lines, or one where at least half the lines look like proxy links. The registry is `registry/sources.json`, up to 300 sources.
2. **Collect.** The bot downloads all sources in parallel, decodes plain or base64 text and parses `vless://` strictly, without executing anything. It keeps only what the app accepts: `tls` or `reality` security (`none` is dropped), supported transports and flows, and a `pbk` key for REALITY. Duplicates are removed by server identity, ignoring the name and the parameter order. There is no limit on how many configs are kept.
3. **Validate.** This step checks liveness only, not speed, because the runners sit in US/EU data centers. Each config gets a TCP connect, then a real request through it with [xray-knife](https://github.com/lilendian0x00/xray-knife). Every collected config is checked: the work is split into parallel jobs (shards) of about 3000 configs, up to 20 of them, and the results are merged before publishing. Configs that stay alive across many runs rank higher.
4. **Publish.** Ranking puts REALITY before TLS, then sorts by stability, then by delay. The country comes from the egress IP, with GeoLite2 as a fallback. The files are checked against the app's format before the push.

`collect.yml` is scheduled every 3 hours, but GitHub often runs scheduled jobs late or skips them, so in practice the list updates every few hours. The "list updated" badge at the top shows the last update.

### Source rules

These rules apply to pinned sources too.

- **Deleted (HTTP 404 / 410 / 451).** The source is excluded from the run at once. The bot re-checks it every run and takes it back on the first 200 response.
- **Stale (not updated for more than 7 days).** The source is excluded until it updates. Freshness comes from the file's last GitHub commit, or the `Last-Modified` header, or the bot's own hash of the content.
- Non-pinned sources are dropped from the registry when they stay deleted, stay stale for over 30 days, or keep failing or returning no configs.
- Every exclusion and its reason is listed in `stats.md` and `meta.json`.

## Add a source

Open a PR that adds a raw link to [`registry/pinned.txt`](registry/pinned.txt), one link per line. Pinned sources are always collected and never removed automatically, though they are skipped while they return 404 / 410 / 451 or are stale.

To block a source for good, add it to [`registry/blocklist.txt`](registry/blocklist.txt).

## ⚠️ Security

Free public servers are run by **unknown people**. They can **log** your traffic, and a TLS server with a swapped certificate can do **MITM**. That's why only `reality` and `tls` are published, but even that guarantees nothing. Don't send passwords, banking data or anything else you care about through these servers.

## Links

- **MyAppVPN app:** [download from Releases](https://github.com/indelingDanil/MyAppVPN/releases/latest)
- **App source code:** [indelingDanil/MyAppVPN-CodeBase](https://github.com/indelingDanil/MyAppVPN-CodeBase)
- **News:** [Telegram channel](https://t.me/myappvpn_dprog)

## Support the project

I plan to keep updating, fixing and improving the app. If it's been useful, a donation helps — I'm saving up for a Mac to try building an iOS version as well.

👉 [Donate via CloudTips (RUB)](https://pay.cloudtips.ru/p/8c90d7a5)

👉 Donate USDT (TON): `UQBi78BaJNXuEXHrP-OhuFAIf29yvpIo1s2agHiTQPB-xWbE`

👉 Donate USDT (TRC20): `TYaGhM39zKXS3g177R6VvBhNLkbsFgYAHj`

👉 Donate BTC (BTC): `bc1qjrtd68le7dcd2h2psqcgmp4nvcp9p092m7ew8z`

👉 Donate USDT (BEP20): `0xB65bb82aB8f85db12b3DEfebE2a4a0b210f097d6`

## Run locally

Needs Go (the version in `go.mod`).

```sh
go run ./cmd/subs collect  --dir work --registry registry          # add --pinned-only for a quick run
go run ./cmd/subs validate --dir work --skip-xray                  # or --xray-knife ./xray-knife
# or sharded, as in CI: one validate per shard, then merge
#   go run ./cmd/subs validate --dir work --skip-xray --shard 0 --shards 2   (and --shard 1)
#   go run ./cmd/subs merge    --dir work --registry registry
go run ./cmd/subs publish  --dir work --out work/out --mmdb GeoLite2-Country.mmdb   # download from the P3TERX mirror, or drop --mmdb
go run ./cmd/subs check    --out work/out
GITHUB_TOKEN=... go run ./cmd/subs discover --registry registry --max-repos 20
go test ./...
```

## Design notes

- **Branches.** The lists and the run state (`state/history.json`, `state/sources.json`) live on the `output` branch, which is force-pushed as one commit per run. On `main`, the bots only commit the weekly registry update from `discover.yml` (plus a rare keepalive commit).
- **Sharding.** `collect.yml` runs as three jobs: `collect` (one job), `validate` (a matrix of `ceil(configs / 3000)` jobs, at most 20, each with its own 40-minute limit) and `publish`. A config always lands in the same shard (hash of its identity). Only the `publish` job updates `state/history.json` and the source stats, so the stability ranking works as with a single job. If a shard fails, its configs are left out of that run and their history stays as it was; `stats.md` says which shard is missing.
- **Keepalive.** GitHub disables cron workflows after 60 days without activity. Once a month `keepalive.yml` re-enables them through the API, and it pushes an empty commit only if `main` has had no commits for 45 days.
- **Pinned tools.** `collect.yml` pins xray-knife to one release and checks its sha256. After each publish, the changed files are purged from the jsDelivr cache.
- **GeoIP.** GeoLite2-Country comes from the [P3TERX/GeoLite.mmdb](https://github.com/P3TERX/GeoLite.mmdb) mirror and is only a fallback. GeoLite2 data © MaxMind, CC BY-SA 4.0.

## Disclaimer

> ⚠️ This is an **educational / research** project. The author **is not responsible** for how the code or the published lists are used. In Russia, VPN services for bypassing blocks are restricted by law and may be considered illegal. Use at your own risk and follow the laws of your country.
>
> All configs belong to their original publishers. This repo only **aggregates publicly available links**. It does not own or run any of the servers and takes no responsibility for them.

Code: [MIT](LICENSE).
