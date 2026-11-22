// Validates offers/offers.json with the same rules the app applies, so a typo is caught on
// GitHub instead of silently hiding an offer. Usage: node offers/validate.mjs offers/offers.json
import fs from "node:fs";

const file = process.argv[2] || "offers/offers.json";
const errors = [];
const warns = [];
let data;
try {
  data = JSON.parse(fs.readFileSync(file, "utf8"));
} catch (e) {
  console.error(`::error file=${file}::Not valid JSON: ${e.message}`);
  process.exit(1);
}

const LANGS = ["ru", "en", "fa", "zh", "tr", "kk", "uz"];
const ICONS = ["game", "vpn", "shop", "ai", "card", "gift"];
const httpsOk = (u) => { try { const x = new URL(u); return x.protocol === "https:" && !!x.hostname; } catch { return false; } };

if (data.schema !== 1) errors.push(`"schema" must be 1`);
if (!Array.isArray(data.offers)) errors.push(`"offers" must be an array`);
const ids = new Set();
for (const [i, o] of (data.offers || []).entries()) {
  const at = `offers[${i}]${o && o.id ? ` (${o.id})` : ""}`;
  if (!o || typeof o !== "object") { errors.push(`${at}: not an object`); continue; }
  if (!o.id || typeof o.id !== "string") errors.push(`${at}: "id" is required`);
  else if (ids.has(o.id)) errors.push(`${at}: duplicate id`);
  else ids.add(o.id);
  if (!o.title) errors.push(`${at}: "title" is required`);
  if (!httpsOk(o.url)) errors.push(`${at}: "url" must be an https:// link`);
  if (!o.text || typeof o.text !== "object" || !Object.keys(o.text).length) errors.push(`${at}: "text" needs at least one language`);
  else {
    const missing = LANGS.filter((l) => !o.text[l]);
    if (missing.length) warns.push(`${at}: no text for ${missing.join(", ")} (falls back to en/ru)`);
  }
  if (o.tg !== undefined && !/^[A-Za-z0-9_]{5,32}$/.test(o.tg)) errors.push(`${at}: "tg" must be a Telegram username (5–32 letters, digits, _)`);
  if (o.tgStart !== undefined && !/^[A-Za-z0-9_-]{1,64}$/.test(o.tgStart)) errors.push(`${at}: "tgStart" has invalid characters`);
  if (o.icon !== undefined && !ICONS.includes(o.icon)) warns.push(`${at}: unknown icon "${o.icon}" (shows the default)`);
  if (o.weight !== undefined && !(Number.isInteger(o.weight) && o.weight >= 1)) errors.push(`${at}: "weight" must be an integer ≥ 1`);
  if (o.until !== undefined && !/^\d{4}-\d{2}-\d{2}$/.test(o.until)) errors.push(`${at}: "until" must be YYYY-MM-DD`);
  if (o.until && new Date(o.until + "T23:59:59Z") < new Date()) warns.push(`${at}: "until" is in the past — the offer is hidden`);
  if (o.trialUrl !== undefined && !httpsOk(o.trialUrl)) errors.push(`${at}: "trialUrl" must be an https:// link`);
  if (o.enabled === false) warns.push(`${at}: disabled`);
}

for (const w of warns) console.log(`::warning file=${file}::${w}`);
for (const e of errors) console.log(`::error file=${file}::${e}`);
console.log(`${(data.offers || []).length} offer(s), ${errors.length} error(s), ${warns.length} warning(s)`);
process.exit(errors.length ? 1 : 0);
