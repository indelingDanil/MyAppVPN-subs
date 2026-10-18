# MyAppVPN-subs

> ⚠️ **Дисклеймер.** Это **учебный / исследовательский проект**. Автор **не несёт ответственности** за то, как
> используются код и опубликованные списки. Сервисы и средства обхода блокировок (VPN) **ограничены или запрещены
> в РФ** — всё, что вы делаете, вы делаете **на свой страх и риск** и в соответствии с законами своей страны.
> Конфигурации принадлежат их первоначальным публикаторам; репозиторий лишь **агрегирует публичные ссылки**,
> найденные в открытом доступе, и не владеет, не управляет и не отвечает ни за один из серверов.

Компаньон-репозиторий Android-клиента **[MyAppVPN](https://github.com/indelingDanil/MyAppVPN)**.
Бот на GitHub Actions сам **находит** публичные бесплатные VLESS-подписки на GitHub, собирает их, убирает дубли,
проверяет живость и публикует очищенные списки. Приложение подтягивает их как встроенную подписку.

📣 **Новости и релизы:** Telegram-канал [@myappvpn_dprog](https://t.me/myappvpn_dprog) ·
📦 **Приложение:** [github.com/indelingDanil/MyAppVPN](https://github.com/indelingDanil/MyAppVPN)

## ❤️ Поддержать проект

Донаты — дело добровольное, но любая копеечка добавляет мотивации продолжать.

- **CloudTips** (карта РФ / СБП): https://pay.cloudtips.ru/p/8c90d7a5
- **USDT (TRC20):** `TB3Qn2guzVdj93kUi43KG8gQY6mruGZbNG`
- **BTC:** `16Bs15Mpu2HAYyVjgiLdwQC1yzLpbf2qyv`
- **ETH (ERC20):** `0x3704ecc83ea8b6a852d63dc82c0badcd761d8e6f`

## Ссылки для подписки

Используйте **jsDelivr** первым — `raw.githubusercontent.com` в РФ часто тормозит/режется.

| Зеркало | `vless.txt` (основной список) |
|---|---|
| jsDelivr (рекомендуется) | https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt |
| githack | https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/vless.txt |
| GitHub raw | https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/vless.txt |

Все файлы лежат в ветке `output` (пути стабильные, ветка перезаписывается одним коммитом при каждом прогоне):

| Файл | Что внутри |
|---|---|
| `vless.txt` / `vless.b64` | основной список: ≤ 500 строк, сначала Reality, потом TLS, ≤ 40 на страну, без RU/UA выходов |
| `reality.txt` / `reality.b64` | только Reality, ≤ 500 строк, ≤ 60 на страну, без RU/UA |
| `by-country/<CC>.txt` | по странам (ISO-код), ≤ 500 строк. `RU.txt` есть — Reality с выходом в РФ нужен для «белых списков». `UA` не публикуется |
| `meta.json` | `{schema:1, updated_at, counts:{total, reality, tls, by_country}, sources_used, excluded:[{url, reason}]}` |
| `stats.md` | человекочитаемая статистика + исключённые источники с причиной |

Формат: обычный текст, по одной ссылке `vless://` на строку (и base64-вариант), каждый файл < 1 МБ.
Имя каждого сервера переписано в вид `🇩🇪 DE reality-0012` — приложение берёт страну из флага в начале.

## Как это работает

1. **discover** (раз в неделю) — поиск репозиториев через GitHub REST (`topic:v2ray-config`, `topic:vless`,
   `vless subscription in:readme,description`, … с `pushed:>=<7 дней назад>`), из README и корня репозитория
   вытаскиваются ссылки на raw-файлы (raw.githubusercontent, github blob/raw, jsDelivr gh, GitLab/Codeberg raw).
   Источник принимается, если в нём ≥ 20 валидных vless-строк или ≥ 50 % строк похожи на прокси-ссылки
   (обычный текст или base64). Реестр — `registry/sources.json` (до 300 источников).
2. **collect** (каждые 3 часа) — параллельная загрузка всех источников (30 с, 2 МБ, 2 повтора), декодирование
   plain/base64/url-safe base64, строгий разбор `vless://` (ничего не исполняется), **тот же фильтр, что в приложении**:
   transport ∈ {tcp, raw, ws, grpc, httpupgrade, xhttp, splithttp, kcp}, security ∈ {tls, reality} (none выкидывается),
   flow ∈ {"", xtls-rprx-vision, xtls-rprx-vision-udp443}, у reality обязателен `pbk`. Дубли убираются по ключу
   `uuid@host:port + type + security + sni + pbk + path/serviceName` (без учёта имени и порядка параметров).
3. **validate** — только живость (раннеры в США/ЕС, скорость не фильтруем): TCP-connect 2 с, затем реальная
   задержка через [xray-knife](https://github.com/lilendian0x00/xray-knife) (`https://www.gstatic.com/generate_204`, 8 с).
   История стабильности (`state/history.json`, ветка `output`) поднимает выше конфиги, которые живы много прогонов подряд.
4. **publish** — ранжирование (Reality → TLS, затем стабильность, затем задержка), страна по egress-IP
   (от xray-knife) или по GeoLite2, запись файлов, `meta.json`, `stats.md`, проверка контракта приложения (`subs check`).

### Правила для источников (касаются и закреплённых)

- **Удалён (HTTP 404 / 410 / 451)** — источник сразу исключается из прогона и помечается `dead_since`.
  Такие источники перепроверяются каждый прогон и возвращаются при первом ответе 200.
  Незакреплённые удаляются из реестра, если при еженедельном discover всё ещё 404; закреплённые остаются в `pinned.txt`.
- **Устарел (не обновлялся > 7 дней)** — исключается как stale. Свежесть берётся, по порядку:
  (a) дата последнего коммита в этот файл через GitHub API (`/commits?path=…&per_page=1`; запрос делается только когда
  содержимое изменилось или даты ещё нет — так вызовов мало), (b) заголовок `Last-Modified`,
  (c) собственный sha256 тела + `content_changed_at` (судим только после 7+ дней наблюдения).
  В `sources.json` пишутся `last_updated`, `freshness_method`, `stale`. Устаревший источник возвращается сам, как только
  обновится; незакреплённые, устаревшие > 30 дней, удаляются.
- Также удаляются (кроме закреплённых): `fail_streak ≥ 5`, 3 прогона подряд без валидных строк, нет успешной загрузки 30 дней.
- Исключённые источники с причиной (`404 …` / `stale Nd`) видны в `stats.md` и `meta.json` → `excluded`.

## Добавить источник

Откройте PR, добавив raw-ссылку в [`registry/pinned.txt`](registry/pinned.txt) (одна ссылка на строку).
Закреплённые источники всегда участвуют в сборе и не удаляются автоматически (но временно исключаются, если 404 или устарели).
Чтобы что-то **никогда** не собиралось — PR в [`registry/blocklist.txt`](registry/blocklist.txt).

## ⚠️ Безопасность

Бесплатные публичные серверы держат **неизвестные люди**. Они могут **логировать** ваш трафик, а TLS-серверы с
подменой — делать **MITM**. Поэтому публикуются только `reality` и `tls` (без `none`), но это не гарантия.
Не передавайте через такие серверы пароли, банковские данные и всё, что вам дорого.

## Локальный запуск

```sh
go run ./cmd/subs collect  --dir work --registry registry            # --pinned-only для быстрого прогона
go run ./cmd/subs validate --dir work --skip-xray                      # или --xray-knife ./xray-knife
go run ./cmd/subs publish  --dir work --out work/out --mmdb GeoLite2-Country.mmdb
go run ./cmd/subs check    --out work/out
GITHUB_TOKEN=... go run ./cmd/subs discover --registry registry --max-repos 20
go test ./...
```

---

## English

> ⚠️ **Disclaimer.** This is an **educational / research project**. The author is **not responsible** for how the code
> or the published lists are used. VPN / censorship-circumvention services are **restricted or illegal in the Russian
> Federation** — use at your own risk and according to your local laws. All configs belong to their original
> publishers; this repo only **aggregates publicly available links** and does not own or operate any server.

Companion repo of the Android client **[MyAppVPN](https://github.com/indelingDanil/MyAppVPN)**. A GitHub Actions bot
discovers free public VLESS subscriptions on GitHub, collects and dedupes them, keeps only what the app accepts
(tls/reality only), checks liveness (TCP + real delay via xray-knife; no speed filtering) and publishes cleaned lists
to the `output` branch every 3 hours:

- https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt (preferred — raw.githubusercontent is often throttled in RU)
- https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/vless.txt
- https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/vless.txt

Also `vless.b64`, `reality.txt/.b64`, `by-country/<CC>.txt`, `meta.json`, `stats.md`. Remarks are rewritten to
`<flag> <CC> <security>-<NNNN>`. RU and UA exits are excluded from the main files (`by-country/RU.txt` exists for whitelist use).

**Source rules (pinned included):** HTTP 404/410/451 → excluded at once, marked `dead_since`, re-checked every run,
non-pinned ones pruned when the weekly discover still gets 404. Not updated for > 7 days (GitHub last commit →
`Last-Modified` → own body hash after 7 days of observation) → excluded as stale until updated; non-pinned stale > 30 days
are pruned. Exclusions are listed with reasons in `stats.md` / `meta.json`.

**Add a source:** PR a raw URL to `registry/pinned.txt`. **Security:** free servers may log or MITM your traffic.

**News:** Telegram [@myappvpn_dprog](https://t.me/myappvpn_dprog). **Support the project** (voluntary):
[CloudTips](https://pay.cloudtips.ru/p/8c90d7a5) · USDT TRC20 `TB3Qn2guzVdj93kUi43KG8gQY6mruGZbNG` ·
BTC `16Bs15Mpu2HAYyVjgiLdwQC1yzLpbf2qyv` · ETH `0x3704ecc83ea8b6a852d63dc82c0badcd761d8e6f`

### Design notes

- `state/history.json` and `state/sources.json` (per-run stats) live on the **`output` branch**, which is force-pushed
  as a single commit every run anyway — so `main` gets no churn every 3 hours. `discover.yml` (weekly) merges the latest
  stats into `registry/sources.json`, prunes, adds new sources and commits to `main` once a week.
- `keepalive.yml` (monthly) re-enables the scheduled workflows through the REST API and, only if `main` had no commit
  for 45 days, pushes an empty commit — GitHub disables cron workflows after 60 days of inactivity.
- GeoLite2-Country is downloaded from the [P3TERX/GeoLite.mmdb](https://github.com/P3TERX/GeoLite.mmdb) mirror (no
  license key). It is only a fallback: the country normally comes from the egress IP reported by xray-knife.
  GeoLite2 data © MaxMind, CC BY-SA 4.0.

Code: MIT (see [LICENSE](LICENSE)).
