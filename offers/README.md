# Offers

`offers.json` is the list of partner offers the MyAppVPN app shows on the **"Проверенные
платные подписки"** page (linked from the Home screen and the Support tab). Every offer here
was tested by the author together with MyAppVPN (usually on a trial the partner gave): the
subscription loads, servers connect, traffic flows. Changes reach the app without a new release.

The app downloads this file at most every 12 hours, from the first mirror that answers:

- https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@main/offers/offers.json
- https://raw.githack.com/indelingDanil/MyAppVPN-subs/main/offers/offers.json
- https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/main/offers/offers.json

It is a plain GET of a static file: no IDs, no cookies, no click tracking. If the file is
broken or unreachable, the app keeps the last good copy.

## How to edit

1. Open `offers/offers.json` on GitHub and press the pencil icon.
2. Copy an existing `{ … }` block, change the fields, commit.
3. The **offers** workflow checks the file and purges the jsDelivr cache. A red ✗ next to the
   commit means a mistake — open the run, the error names the offer and the field.

## Fields

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Stable, unique, e.g. `proxen`. |
| `title` | yes | Shown as is, not translated. |
| `text` | yes | Language → text: `ru`, `en`, `fa`, `zh`, `tr`, `kk`, `uz`. Falls back to `en`, then `ru`. |
| `url` | yes | `https://` only, the referral link. Opened in the browser. |
| `category` | no | `vpn` → "VPN-подписки" section; anything else → "Другие сервисы". |
| `subscription` | no | `true` (default for `vpn`): the card says the subscription can be added to the app. |
| `testedAt` | no | `YYYY-MM-DD` — when the author last tested the service with MyAppVPN (usually on a trial). Shown as "Проверено с MyAppVPN · 22.11.2026". |
| `features` | no | Language → short text of what was checked, e.g. `"VLESS REALITY, Hysteria2 · YouTube без рекламы"`. |
| `tg`, `tgStart` | no | Telegram bot/channel username and `start` parameter (referral). Opened in the Telegram app. |
| `icon` | no | `game`, `vpn`, `shop`, `ai`, `card`, `gift`. Default `shop`. |
| `weight` | no | Integer ≥ 1. Higher = higher on the page. |
| `until` | no | `YYYY-MM-DD`. The offer disappears after this date. |
| `enabled` | no | `false` hides the offer without deleting it. |

## Example: VPN tested on a trial

```json
{
  "id": "example-vpn",
  "title": "Example VPN",
  "category": "vpn",
  "text": { "ru": "Стабильный VLESS + Hysteria2, YouTube без рекламы", "en": "Stable VLESS + Hysteria2, ad-free YouTube" },
  "features": { "ru": "VLESS REALITY, Hysteria2 · YouTube без рекламы", "en": "VLESS REALITY, Hysteria2 · ad-free YouTube" },
  "testedAt": "2026-11-22",
  "url": "https://example.com/?ref=XXXX",
  "tg": "ExampleBot",
  "tgStart": "ref_XXXX",
  "icon": "vpn",
  "weight": 10
}
```
