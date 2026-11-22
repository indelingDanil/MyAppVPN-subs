# Offers

`offers.json` is the list of partner offers the MyAppVPN app shows under the connect button
(one random offer) and in the Support tab (all of them). Every offer here was tested by the
author together with MyAppVPN. The app downloads this file at most every 12 hours from:

- https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@main/offers/offers.json
- https://raw.githack.com/indelingDanil/MyAppVPN-subs/main/offers/offers.json
- https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/main/offers/offers.json

The app makes no other requests for offers and does not track clicks.

## Fields

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Stable, unique. The ✕ on Home hides this id for 24 hours. |
| `title` | yes | Shown as is, not translated. |
| `text` | yes | Map of language → text: `ru`, `en`, `fa`, `zh`, `tr`, `kk`, `uz`. Falls back to `en`, then `ru`. |
| `url` | yes | `https://` only. Opened in the browser. |
| `tg`, `tgStart` | no | Telegram bot/channel username and `start` parameter. Opened in the Telegram app. |
| `icon` | no | `game`, `vpn`, `shop`, `ai`, `card`, `gift`. Default `shop`. |
| `weight` | no | Integer ≥ 1. Higher = shown more often on Home. |
| `until` | no | `YYYY-MM-DD`. The offer disappears after this date. |
| `enabled` | no | `false` hides the offer without deleting it. |

jsDelivr caches `@main` for up to 12 hours. After editing, purge it:
`https://purge.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@main/offers/offers.json`
