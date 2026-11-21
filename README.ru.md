<h1 align="center">MyAppVPN-subs</h1>

<p align="center"><b>Автособираемая VLESS-подписка: бот раз в несколько часов находит, проверяет и чистит публичные серверы.</b></p>

<p align="center">
  <a href="README.md">English</a> · <b>Русский</b>
</p>

<p align="center">
  <a href="https://github.com/indelingDanil/MyAppVPN-subs/tree/output"><img src="https://img.shields.io/github/last-commit/indelingDanil/MyAppVPN-subs/output?label=list%20updated" alt="Последнее обновление списка"></a>
  <a href="https://github.com/indelingDanil/MyAppVPN-subs/actions/workflows/collect.yml"><img src="https://img.shields.io/github/actions/workflow/status/indelingDanil/MyAppVPN-subs/collect.yml?branch=main&label=collect" alt="Статус workflow collect"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="Лицензия MIT"></a>
  <a href="https://t.me/myappvpn_dprog"><img src="https://img.shields.io/badge/Telegram-channel-26A5E4?logo=telegram&logoColor=white" alt="Telegram-канал"></a>
</p>

## Ссылки для подписки

Используйте **jsDelivr** в первую очередь: `raw.githubusercontent.com` в РФ часто тормозит или блокируется.

| Файл | jsDelivr (рекомендуется) | githack | GitHub raw |
|---|---|---|---|
| `vless.txt` (основной список) | [ссылка](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt) | [ссылка](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/vless.txt) | [ссылка](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/vless.txt) |
| `reality.txt` (только REALITY) | [ссылка](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/reality.txt) | [ссылка](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/reality.txt) | [ссылка](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/reality.txt) |
| `by-country/<CC>.txt`, например `DE` | [ссылка](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/by-country/DE.txt) | [ссылка](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/by-country/DE.txt) | [ссылка](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/by-country/DE.txt) |
| `all.txt` (все живые серверы) | [ссылка](https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/all.txt) | [ссылка](https://raw.githack.com/indelingDanil/MyAppVPN-subs/output/all.txt) | [ссылка](https://raw.githubusercontent.com/indelingDanil/MyAppVPN-subs/output/all.txt) |

Основная ссылка для v2rayNG, Hiddify и похожих клиентов:

```
https://cdn.jsdelivr.net/gh/indelingDanil/MyAppVPN-subs@output/vless.txt
```

В **[MyAppVPN](https://github.com/indelingDanil/MyAppVPN)** она уже встроена как «MyAppVPN (авто-сбор)». Отключается в Настройки → Подписки.

### Что лежит в ветке `output`

| Файл | Что внутри |
|---|---|
| `vless.txt` / `vless.b64` | Основной список: до 500 серверов, сначала REALITY, потом TLS, не больше 40 на страну, без выходов в RU/UA |
| `reality.txt` / `reality.b64` | Только REALITY: до 500 серверов, не больше 60 на страну, без выходов в RU/UA |
| `by-country/<CC>.txt` | Файл на каждую страну (ISO-код), до 500 серверов. `RU.txt` есть — он нужен для сетей с «белыми списками»; `UA` не публикуется |
| `all.txt` / `all.b64` | Все серверы, прошедшие проверку, в том же порядке, без лимита строк и стран, RU включены, UA нет. Может весить несколько МБ: для клиентов, которые тянут большие списки, а не для приложения |
| `meta.json` | Время обновления, счётчики (собрано / живых по TCP / живых по xray / опубликовано, всего / reality / tls / по странам), использованные источники, исключённые источники с причиной |
| `stats.md` | Та же статистика в читаемом виде |

В каждом файле по одной ссылке `vless://` на строку (в `.b64` тот же список в base64). Все файлы, кроме `all.txt`, меньше 1 МБ. Если `all.txt` когда-нибудь перерастёт лимит GitHub в 100 МБ на файл, он делится на `all-1.txt`, `all-2.txt`… (список есть в `meta.json`); jsDelivr не отдаёт файлы больше 20 МБ, так что для такого списка берите ссылку githack или GitHub raw. Имена серверов переписаны в вид `🇩🇪 DE reality-0012`, MyAppVPN берёт страну из флага. Каждый прогон перезаписывает ветку одним коммитом, пути к файлам не меняются.

## Как это работает

```
discover (раз в неделю) → collect → validate → publish   (раз в несколько часов)
```

1. **Discover.** Бот ищет на GitHub репозитории с публичными VLESS-подписками, обновлёнными за последние 7 дней, и вытаскивает из них ссылки на raw-файлы. Источник принимается, если в нём не меньше 20 валидных строк `vless://` или хотя бы половина строк похожа на прокси-ссылки. Реестр — `registry/sources.json`, до 300 источников.
2. **Collect.** Бот параллельно скачивает все источники, декодирует обычный текст и base64 и строго разбирает `vless://`, ничего не исполняя. Остаётся только то, что принимает приложение: security `tls` или `reality` (`none` выкидывается), поддерживаемые транспорты и flow, у REALITY обязателен ключ `pbk`. Дубли убираются по самому серверу, без учёта имени и порядка параметров. Лимита на число конфигов нет.
3. **Validate.** Проверяется только живость, не скорость: раннеры стоят в дата-центрах США/ЕС. Каждый конфиг получает TCP-подключение, потом реальный запрос через него с помощью [xray-knife](https://github.com/lilendian0x00/xray-knife). Проверяется всё, что собрано: работа делится на параллельные задачи (шарды) примерно по 3000 конфигов, до 20 штук, и перед публикацией результаты сводятся вместе. Конфиги, которые живут много прогонов подряд, поднимаются выше.
4. **Publish.** Сначала REALITY, потом TLS, внутри — по стабильности, затем по задержке. Страна определяется по IP выхода, GeoLite2 — запасной вариант. Перед публикацией файлы проверяются на соответствие формату приложения.

В `collect.yml` стоит запуск раз в 3 часа, но GitHub часто запускает задачи по расписанию с опозданием или пропускает их, поэтому на деле список обновляется раз в несколько часов. Время последнего обновления видно на значке «list updated» вверху.

### Правила для источников

Касаются и закреплённых источников.

- **Удалён (HTTP 404 / 410 / 451).** Источник сразу исключается из прогона. Бот перепроверяет его каждый прогон и возвращает при первом ответе 200.
- **Устарел (не обновлялся больше 7 дней).** Источник исключается, пока не обновится. Свежесть берётся из последнего коммита в файл на GitHub, из заголовка `Last-Modified` или из собственного хеша содержимого.
- Незакреплённые источники удаляются из реестра, если остаются удалёнными, больше 30 дней не обновляются или постоянно не загружаются либо не дают конфигов.
- Все исключения с причиной видны в `stats.md` и `meta.json`.

## Добавить источник

Откройте PR со ссылкой на raw-файл в [`registry/pinned.txt`](registry/pinned.txt), по одной ссылке на строку. Закреплённые источники собираются всегда и не удаляются автоматически, но временно пропускаются, пока отдают 404 / 410 / 451 или устарели.

Чтобы источник не собирался никогда, добавьте его в [`registry/blocklist.txt`](registry/blocklist.txt).

## ⚠️ Безопасность

Бесплатные публичные серверы держат **неизвестные люди**. Они могут **логировать** ваш трафик, а TLS-сервер с подменённым сертификатом может делать **MITM**. Поэтому публикуются только `reality` и `tls`, но и это ничего не гарантирует. Не передавайте через такие серверы пароли, банковские данные и всё, что вам дорого.

## Ссылки

- **Приложение MyAppVPN:** [скачать в Releases](https://github.com/indelingDanil/MyAppVPN/releases/latest)
- **Исходный код приложения:** [indelingDanil/MyAppVPN-CodeBase](https://github.com/indelingDanil/MyAppVPN-CodeBase)
- **Новости:** [Telegram-канал](https://t.me/myappvpn_dprog)

## Поддержать проект

Планирую и дальше обновлять приложение: чинить ошибки и добавлять новое. Если оно оказалось полезным — буду рад донату: коплю на Mac, чтобы попробовать сделать версию для iOS.

👉 [Задонатить через CloudTips (RUB)](https://pay.cloudtips.ru/p/8c90d7a5)

👉 USDT (TRC20): `TB3Qn2guzVdj93kUi43KG8gQY6mruGZbNG`

👉 BTC (BTC): `16Bs15Mpu2HAYyVjgiLdwQC1yzLpbf2qyv`

👉 ETH (ERC20): `0x3704ecc83ea8b6a852d63dc82c0badcd761d8e6f`

## Локальный запуск

Нужен Go (версия указана в `go.mod`).

```sh
go run ./cmd/subs collect  --dir work --registry registry          # --pinned-only для быстрого прогона
go run ./cmd/subs validate --dir work --skip-xray                  # или --xray-knife ./xray-knife
# или по шардам, как в CI: validate на каждый шард, потом merge
#   go run ./cmd/subs validate --dir work --skip-xray --shard 0 --shards 2   (и --shard 1)
#   go run ./cmd/subs merge    --dir work --registry registry
go run ./cmd/subs publish  --dir work --out work/out --mmdb GeoLite2-Country.mmdb   # скачайте с зеркала P3TERX или уберите --mmdb
go run ./cmd/subs check    --out work/out
GITHUB_TOKEN=... go run ./cmd/subs discover --registry registry --max-repos 20
go test ./...
```

## Как устроено

- **Ветки.** Списки и состояние прогонов (`state/history.json`, `state/sources.json`) живут в ветке `output`, которую каждый прогон перезаписывает одним коммитом через force-push. Боты коммитят в `main` только еженедельное обновление реестра от `discover.yml` (и изредка keepalive-коммит).
- **Шарды.** `collect.yml` состоит из трёх задач: `collect` (одна), `validate` (матрица из `ceil(конфиги / 3000)` задач, не больше 20, у каждой свой лимит 40 минут) и `publish`. Конфиг всегда попадает в один и тот же шард (по хешу самого сервера). `state/history.json` и статистику источников обновляет только `publish`, поэтому рейтинг стабильности считается так же, как при одной задаче. Если шард упал, его конфиги в этот прогон не попадают, а их история не меняется; в `stats.md` видно, какого шарда не хватило.
- **Keepalive.** GitHub отключает cron-workflow после 60 дней без активности. Раз в месяц `keepalive.yml` включает их обратно через API, а пустой коммит делает, только если в `main` 45 дней не было коммитов.
- **Закреплённые инструменты.** В `collect.yml` xray-knife зафиксирован на одном релизе и проверяется по sha256. После каждой публикации изменённые файлы сбрасываются из кеша jsDelivr.
- **GeoIP.** GeoLite2-Country скачивается с зеркала [P3TERX/GeoLite.mmdb](https://github.com/P3TERX/GeoLite.mmdb) и используется только как запасной вариант. Данные GeoLite2 © MaxMind, CC BY-SA 4.0.

## Дисклеймер

> ⚠️ Это **учебный / исследовательский** проект. Автор **не несёт ответственности** за то, как используются код и опубликованные списки. Помните: в РФ VPN-сервисы для обхода блокировок ограничены законом и могут считаться незаконными. Используйте на свой страх и риск и соблюдайте законы своей страны.
>
> Все конфигурации принадлежат их первоначальным публикаторам. Репозиторий лишь **агрегирует публичные ссылки** и не владеет, не управляет и не отвечает ни за один из серверов.

Код: [MIT](LICENSE).
