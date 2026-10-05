#!/usr/bin/env node
// Static checks for a Paseo plugin directory (0.8+ runtime-entry format).
// Usage: node lint-plugin.mjs <plugin-dir> [--json]
// Exit code: 0 = no errors, 1 = errors found, 2 = bad invocation.
// Zero dependencies. Heuristic: complements, never replaces, `npm run typecheck` and `paseo plugin reload`.

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { builtinModules } from "node:module";
import path from "node:path";

const args = process.argv.slice(2);
const asJson = args.includes("--json");
const dirArg = args.find((a) => !a.startsWith("--"));
if (!dirArg) {
  console.error("Usage: node lint-plugin.mjs <plugin-dir> [--json]");
  process.exit(2);
}
const root = path.resolve(dirArg);
if (!existsSync(root) || !statSync(root).isDirectory()) {
  console.error(`Not a directory: ${root}`);
  process.exit(2);
}

const findings = [];
const report = (level, file, line, message) =>
  findings.push({ level, file: file ? toPosix(path.relative(root, file)) : null, line: line ?? null, message });
const toPosix = (p) => p.split(path.sep).join("/");

// ---------- constants ----------

const ID_RE = /^[a-z][a-z0-9-]*$/;
const RPC_RE = /^[a-z][a-z0-9._-]*$/;
const SETTINGS_RE = /^[a-z][a-z0-9_-]*$/;
const CODE_EXT = /\.(ts|tsx|js|jsx|mjs|cjs|mts|cts)$/;
const CLIENT_ENTRIES = ["index.client.ts", "index.client.tsx"];
const SERVER_ENTRIES = ["index.server.ts", "index.server.tsx"];

const CLIENT_ALLOWED = new Set([
  "@getpaseo/plugin",
  "@getpaseo/plugin/client",
  "@getpaseo/plugin/client/ui",
  "@getpaseo/plugin/client/react-native",
  "@tanstack/react-query",
  "react",
  "react/jsx-runtime",
  "react-native",
  "zod",
]);
const SERVER_SDK = new Set([
  "@getpaseo/plugin",
  "@getpaseo/plugin/server",
  "@getpaseo/plugin/server/provider",
  "@getpaseo/plugin/server/acp",
  "@getpaseo/plugin/server/usage",
  "zod",
]);
const SHARED_ALLOWED = new Set(["@getpaseo/plugin", "zod"]);
const LEGACY_SPECIFIERS = {
  "@getpaseo/plugin/react-native": "@getpaseo/plugin/client/react-native",
  "@getpaseo/plugin/ui": "@getpaseo/plugin/client/ui",
  "@getpaseo/plugin/provider": "@getpaseo/plugin/server/provider",
  "@getpaseo/plugin/acp": "@getpaseo/plugin/server/acp",
};
const CLIENT_BANNED = {
  "lucide-react-native": "use `icon: \"<LucideName>\"` or <Icon name=…> from @getpaseo/plugin/client/react-native",
  "react-native-svg": "Paseo renders icons; use <Icon> or Lucide names",
  "react-dom": "client code must be React Native, not DOM",
  "@gorhom/bottom-sheet": "use Modal / ScrollView / FlatList from @getpaseo/plugin/client/react-native",
  "react-native-gesture-handler": "Paseo owns sheet gestures; use SDK ScrollView/FlatList",
};
const NODE_BUILTINS = new Set(builtinModules.flatMap((m) => [m, `node:${m}`]));
const EVENTS = new Set([
  "agent.created",
  "agent.turn_started",
  "agent.turn_ended",
  "agent.permission_requested",
  "agent.permission_resolved",
  "agent.archived",
  "workspace.created",
  "workspace.archived",
]);
const BEFORE_HOOKS = new Set(["agent.create", "agent.session_open", "workspace.create"]);
const CLIENT_REGISTRATIONS = [
  "addScreen",
  "addSidebarHeaderItem",
  "addSidebarFooterItem",
  "addSidebarItem",
  "addWorkspacePanel",
  "addCommandCenterItem",
  "addSlashCommand",
  "addHeaderButton",
  "addComposerPill",
  "addSettingsScreen",
  "addTheme",
  "addTimelineTransformer",
  "addTimelineRenderer",
  "addAttachmentSource",
];
const SERVER_REGISTRATIONS = ["registerProvider", "registerUsageSource", "registerSettings"];
const HTML_TAGS =
  "div|span|button|input|a|p|img|ul|ol|li|h[1-6]|form|label|select|option|textarea|table|thead|tbody|tr|td|th|section|header|footer|nav|main|article|aside|iframe|canvas|video|audio|br|hr|strong|em|code|pre";

// ---------- helpers ----------

const read = (f) => readFileSync(f, "utf8");

/** Replace comments with spaces (newlines kept) so offsets/line numbers stay valid; strings untouched. */
function stripComments(src) {
  let out = "";
  let i = 0;
  let quote = null;
  while (i < src.length) {
    const c = src[i];
    const n = src[i + 1];
    if (quote) {
      out += c;
      if (c === "\\") {
        out += n ?? "";
        i += 2;
        continue;
      }
      if (c === quote) quote = null;
      i++;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      quote = c;
      out += c;
      i++;
      continue;
    }
    if (c === "/" && n === "/") {
      while (i < src.length && src[i] !== "\n") {
        out += " ";
        i++;
      }
      continue;
    }
    if (c === "/" && n === "*") {
      while (i < src.length && !(src[i] === "*" && src[i + 1] === "/")) {
        out += src[i] === "\n" ? "\n" : " ";
        i++;
      }
      out += "  ";
      i += 2;
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

const lineOf = (text, index) => text.slice(0, index).split("\n").length;

function walk(dir) {
  if (!existsSync(dir)) return [];
  const out = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const full = path.join(dir, name);
    const st = statSync(full);
    if (st.isDirectory()) out.push(...walk(full));
    else if (CODE_EXT.test(name)) out.push(full);
  }
  return out;
}

function zoneOf(file) {
  const rel = toPosix(path.relative(root, file));
  if (rel.startsWith("..")) return "outside";
  const top = rel.split("/")[0];
  if (top === "client" || CLIENT_ENTRIES.includes(rel)) return "client";
  if (top === "server" || SERVER_ENTRIES.includes(rel)) return "server";
  if (top === "shared") return "shared";
  return "root";
}

function importsOf(text) {
  const res = [];
  const patterns = [
    /\bfrom\s*(["'])([^"']+)\1/g,
    /\bimport\s*(["'])([^"']+)\1/g,
    /\bimport\s*\(\s*(["'])([^"']+)\1\s*\)/g,
    /\brequire\s*\(\s*(["'])([^"']+)\1\s*\)/g,
  ];
  for (const re of patterns) {
    for (const m of text.matchAll(re)) res.push({ spec: m[2], index: m.index });
  }
  return res;
}

const packageName = (spec) =>
  spec.startsWith("@") ? spec.split("/").slice(0, 2).join("/") : spec.split("/")[0];

function minPaseoVersion(range) {
  const m = /(?:>=|\^|~|=)?\s*v?(\d+)\.(\d+)(?:\.(\d+))?/.exec(range ?? "");
  return m ? [Number(m[1]), Number(m[2]), Number(m[3] ?? 0)] : null;
}
const lt = (a, b) => a[0] < b[0] || (a[0] === b[0] && (a[1] < b[1] || (a[1] === b[1] && a[2] < b[2])));

// ---------- manifest ----------

const manifestPath = path.join(root, "paseo-plugin.json");
let manifest = null;
if (!existsSync(manifestPath)) {
  report("error", null, null, "Missing paseo-plugin.json at the plugin root.");
} else {
  try {
    manifest = JSON.parse(read(manifestPath));
  } catch (e) {
    report("error", manifestPath, null, `paseo-plugin.json is not valid JSON: ${e.message}`);
  }
}
if (manifest) {
  const known = new Set(["id", "description", "requirements", "build"]);
  for (const k of Object.keys(manifest)) {
    if (!known.has(k)) report("warn", manifestPath, null, `Unknown manifest field "${k}".`);
  }
  if (typeof manifest.id !== "string" || !ID_RE.test(manifest.id)) {
    report("error", manifestPath, null, `"id" must match ${ID_RE} (got ${JSON.stringify(manifest.id)}).`);
  }
  if ("description" in manifest && (typeof manifest.description !== "string" || !manifest.description.trim())) {
    report("error", manifestPath, null, '"description" must be a non-empty string when present.');
  }
  const req = manifest.requirements;
  if (req !== undefined && (typeof req !== "object" || req === null || Array.isArray(req))) {
    report("error", manifestPath, null, '"requirements" must be an object.');
  } else {
    for (const k of Object.keys(req ?? {})) {
      if (k !== "paseo") report("error", manifestPath, null, `Unknown requirements key "${k}" (only "paseo" is supported).`);
    }
    if (req?.paseo === undefined) {
      report("error", manifestPath, null, 'Missing "requirements.paseo" → treated as <0.8.0 and rejected by Paseo 0.8+. Add e.g. { "paseo": ">=0.8.0" }.');
    } else if (typeof req.paseo !== "string" || !req.paseo.trim()) {
      report("error", manifestPath, null, '"requirements.paseo" must be a non-empty semver range.');
    } else if (!minPaseoVersion(req.paseo)) {
      report("warn", manifestPath, null, `Could not parse a minimum version from "${req.paseo}".`);
    }
  }
  if ("build" in manifest) {
    const ok =
      Array.isArray(manifest.build) &&
      manifest.build.every((cmd) => Array.isArray(cmd) && cmd.length > 0 && cmd.every((a) => typeof a === "string" && a.length > 0));
    if (!ok) report("error", manifestPath, null, '"build" must be a list of non-empty argv arrays, e.g. [["npm","ci","--omit=dev"]].');
    else
      report("info", manifestPath, null, '"build" runs (no shell) on every install and update. Keep it only if the staged checkout needs preparation.');
  }
}

// ---------- entries & root modules ----------

const rootFiles = readdirSync(root).filter((f) => statSync(path.join(root, f)).isFile());
const hasClient = CLIENT_ENTRIES.some((f) => rootFiles.includes(f));
const hasServer = SERVER_ENTRIES.some((f) => rootFiles.includes(f));
if (!hasClient && !hasServer) {
  if (rootFiles.includes("index.ts") || rootFiles.includes("index.tsx")) {
    report("error", path.join(root, "index.ts"), null, "Legacy single index.ts entry: migrate to index.client.tsx / index.server.ts (see troubleshooting-and-migration.md).");
  } else {
    report("error", null, null, "Plugin entry points are missing: add index.client.tsx and/or index.server.ts.");
  }
}
for (const f of rootFiles) {
  if (!CODE_EXT.test(f) || CLIENT_ENTRIES.includes(f) || SERVER_ENTRIES.includes(f)) continue;
  const full = path.join(root, f);
  if (f === "index.ts" || f === "index.tsx") {
    if (hasClient || hasServer) report("error", full, null, "Delete the legacy index.ts; Paseo does not load it.");
  } else if (/\.config\.[cm]?[jt]s$/.test(f) || f.endsWith(".d.ts")) {
    report("warn", full, null, "Root code file: fine for tooling only if no plugin entry imports it.");
  } else {
    report("error", full, null, "Plugin modules belong in client/, server/, or shared/ (code at the root is a compile error).");
  }
}

// ---------- source files ----------

const files = [
  ...CLIENT_ENTRIES.concat(SERVER_ENTRIES).filter((f) => rootFiles.includes(f)).map((f) => path.join(root, f)),
  ...walk(path.join(root, "client")),
  ...walk(path.join(root, "server")),
  ...walk(path.join(root, "shared")),
];

const ids = new Map(); // kind -> Map(id -> [file,line])
const noteId = (kind, id, file, line) => {
  if (!ids.has(kind)) ids.set(kind, new Map());
  const m = ids.get(kind);
  if (m.has(id)) report("warn", file, line, `Duplicate ${kind} id "${id}" (also at ${m.get(id)}).`);
  else m.set(id, `${toPosix(path.relative(root, file))}:${line}`);
};
let usesUsageSource = null;
const svgIcons = [];

for (const file of files) {
  const zone = zoneOf(file);
  const raw = read(file);
  const text = stripComments(raw);
  const rel = toPosix(path.relative(root, file));
  const isTsx = /\.(tsx|jsx)$/.test(file);
  const isWebShim = /^client\/web\.(ts|tsx)$/.test(rel);

  // entries default-export a contribution function
  if ([...CLIENT_ENTRIES, ...SERVER_ENTRIES].includes(rel) && !/\bexport\s+default\b/.test(text)) {
    report("error", file, null, "Entry must `export default function contribute(ctx) { …; return cleanup; }`.");
  }

  // imports
  for (const { spec, index } of importsOf(text)) {
    const line = lineOf(text, index);
    if (spec.startsWith(".")) {
      const target = path.resolve(path.dirname(file), spec);
      const tz = zoneOf(target);
      if (zone === "client" && tz === "server")
        report("error", file, line, `server-only module imported into the client bundle ("${spec}"). Move the work behind an RPC; import its contract from shared/.`);
      if (zone === "server" && tz === "client")
        report("error", file, line, `client-only module imported into the server bundle ("${spec}"). Register it from index.client.tsx instead.`);
      if (zone === "shared" && (tz === "client" || tz === "server"))
        report("error", file, line, `shared/ may import only shared/ ("${spec}" is in ${tz}/).`);
      if (tz === "root" || tz === "outside")
        report("warn", file, line, `Import "${spec}" resolves outside client/, server/, shared/.`);
      continue;
    }
    const pkg = packageName(spec);
    if (spec === "@paseo/plugin" || spec.startsWith("@paseo/plugin/")) {
      report("error", file, line, `"${spec}": the pre-0.8 @paseo/plugin scope was removed; use @getpaseo/plugin.`);
      continue;
    }
    if (LEGACY_SPECIFIERS[spec]) {
      report("error", file, line, `"${spec}" was removed in 0.8; use "${LEGACY_SPECIFIERS[spec]}".`);
      continue;
    }
    if (spec === "@getpaseo/plugin/client/host") {
      report("error", file, line, '"@getpaseo/plugin/client/host" is private to the app host.');
      continue;
    }
    const isNode = NODE_BUILTINS.has(spec) || NODE_BUILTINS.has(spec.split("/")[0]);
    if (zone === "client") {
      if (isNode) report("error", file, line, `Node module "${spec}" cannot be imported into the client bundle. Move it to server/ behind an RPC.`);
      else if (spec.startsWith("@getpaseo/plugin/server")) report("error", file, line, `"${spec}" is server-only.`);
      else if (CLIENT_BANNED[pkg]) report("error", file, line, `Do not import "${pkg}": ${CLIENT_BANNED[pkg]}.`);
      else if (!CLIENT_ALLOWED.has(spec))
        report("warn", file, line, `"${spec}" is not a host-provided client module; it must be bundled from your dependencies and be React Native-safe, or loading fails with 'Module … is not available'.`);
    } else if (zone === "server") {
      if (pkg === "react" || pkg === "react-native" || spec.startsWith("@getpaseo/plugin/client"))
        report("error", file, line, `"${spec}" cannot be imported into the server bundle.`);
      else if (pkg === "@getpaseo/plugin" && !SERVER_SDK.has(spec)) report("warn", file, line, `Unknown SDK entry "${spec}" for server code.`);
    } else if (zone === "shared") {
      if (isNode || pkg === "react" || pkg === "react-native" || spec.startsWith("@getpaseo/plugin/"))
        report("error", file, line, `shared/ must stay runtime-neutral: "${spec}" is not allowed (types included).`);
      else if (!SHARED_ALLOWED.has(spec)) report("warn", file, line, `shared/ imports "${spec}"; make sure it is runtime-neutral and installed.`);
    }
  }

  // registrations in the wrong runtime
  for (const name of CLIENT_REGISTRATIONS) {
    const re = new RegExp(`\\.${name}\\s*\\(`, "g");
    for (const m of text.matchAll(re)) {
      if (zone === "server" || zone === "shared")
        report("error", file, lineOf(text, m.index), `${name}() is a client registration; call it from index.client.tsx or a client/ function.`);
    }
  }
  for (const name of SERVER_REGISTRATIONS) {
    for (const m of text.matchAll(new RegExp(`\\.${name}\\s*\\(`, "g"))) {
      if (zone !== "server") report("error", file, lineOf(text, m.index), `${name}() is a server registration; call it from index.server.ts.`);
      if (name === "registerUsageSource") usesUsageSource = [file, lineOf(text, m.index)];
    }
  }

  // lifecycle hook names
  for (const m of text.matchAll(/\.(on|before)\s*\(\s*(["'])((?:agent|workspace)\.[^"']*)\2/g)) {
    const [, kind, , name] = m;
    const line = lineOf(text, m.index);
    if (zone !== "server") report("error", file, line, `Lifecycle hooks are registered on the server context (index.server.ts), found ${kind}("${name}").`);
    const known = kind === "on" ? EVENTS : BEFORE_HOOKS;
    if (!known.has(name))
      report("error", file, line, `Unknown ${kind === "on" ? "event" : "before-hook"} "${name}". Known: ${[...known].join(", ")}.`);
  }

  // ID formats
  const idCalls = [
    ["addScreen", "screen"],
    ["addSidebarHeaderItem", "sidebar item"],
    ["addSidebarFooterItem", "sidebar item"],
    ["addSidebarItem", "sidebar item"],
    ["addWorkspacePanel", "workspace panel"],
    ["addCommandCenterItem", "command center item"],
    ["addSettingsScreen", "settings screen"],
    ["addHeaderButton", "header button"],
    ["addComposerPill", "composer pill"],
  ];
  for (const [call, kind] of idCalls) {
    for (const m of text.matchAll(new RegExp(`\\.${call}\\s*\\(\\s*\\{[\\s\\S]{0,300}?\\bid\\s*:\\s*(["'])([^"']*)\\1`, "g"))) {
      const id = m[2];
      const line = lineOf(text, m.index);
      if (!ID_RE.test(id)) report("error", file, line, `${kind} id "${id}" must match ${ID_RE}.`);
      if (kind !== "header button" && kind !== "composer pill") noteId(kind, id, file, line);
    }
  }
  for (const m of text.matchAll(/\.addSlashCommand\s*\(\s*\{[\s\S]{0,300}?\bname\s*:\s*(["'])([^"']*)\1/g)) {
    const line = lineOf(text, m.index);
    if (m[2].startsWith("/")) report("error", file, line, `Slash command name "${m[2]}" must omit the leading slash.`);
    else if (!ID_RE.test(m[2])) report("error", file, line, `Slash command name "${m[2]}" must match ${ID_RE}.`);
    noteId("slash command", m[2], file, line);
  }
  for (const m of text.matchAll(/\bdefineRpc\s*\(\s*\{[\s\S]{0,200}?\bname\s*:\s*(["'])([^"']*)\1/g)) {
    const line = lineOf(text, m.index);
    if (!RPC_RE.test(m[2])) report("error", file, line, `RPC name "${m[2]}" must match ${RPC_RE}.`);
    noteId("rpc", m[2], file, line);
    if (zone !== "shared") report("warn", file, line, "Define RPC contracts in shared/ so both runtimes import the same contract.");
  }
  for (const m of text.matchAll(/\bdefineSettings\s*\(\s*\{[\s\S]{0,200}?\bid\s*:\s*(["'])([^"']*)\1/g)) {
    const line = lineOf(text, m.index);
    if (!SETTINGS_RE.test(m[2])) report("error", file, line, `Settings id "${m[2]}" must match ${SETTINGS_RE}.`);
    if (zone !== "shared") report("warn", file, line, "Define settings documents in shared/.");
  }
  for (const m of text.matchAll(/\bdefineAttachmentSource\s*\(\s*\{[\s\S]{0,200}?\bid\s*:\s*(["'])([^"']*)\1/g)) {
    const line = lineOf(text, m.index);
    if (!ID_RE.test(m[2])) report("error", file, line, `Attachment source id "${m[2]}" must match ${ID_RE}.`);
    if (zone !== "shared") report("warn", file, line, "Define attachment sources in shared/.");
  }

  // provider / usage-source SVG icons
  if (zone === "server") {
    for (const m of text.matchAll(/\bicon\s*:\s*(["'])([^"']+\.svg)\1/g)) svgIcons.push([m[2], file, lineOf(text, m.index)]);
  }

  // cross-platform rules for client code
  if (zone === "client" && !isWebShim) {
    if (/\/\/\/\s*<reference\s+lib=["']dom["']/i.test(raw))
      report("error", file, null, 'Do not add `/// <reference lib="dom" />`; client code must run on iOS/Android.');
    for (const m of text.matchAll(/(?<![\w$.])(window|document|localStorage|sessionStorage|navigator)(?:\??\.[A-Za-z_$]|\[)/g))
      report("error", file, lineOf(text, m.index), `DOM global "${m[1]}" breaks on iOS/Android. Gate browser code in client/web.ts behind Platform.OS.`);
    if (isTsx) {
      for (const m of text.matchAll(new RegExp(`<(${HTML_TAGS})(?=[\\s/>])`, "g")))
        report("error", file, lineOf(text, m.index), `HTML element <${m[1]}> — use React Native View/Text/Pressable/ScrollView/TextInput.`);
      for (const m of text.matchAll(/\b(className|onClick|onDoubleClick|onMouse(?:Enter|Leave|Down|Up|Over|Move))\s*=/g))
        report("error", file, lineOf(text, m.index), `"${m[1]}" is DOM-only — use style objects from theme.colors and onPress.`);
    }
    if (!/\.addTheme\s*\(/.test(text)) {
      for (const m of text.matchAll(/(["'`])(#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})|rgba?\([^)]*\))\1/g))
        report("warn", file, lineOf(text, m.index), `Hardcoded color ${m[2]} — use theme.colors.* so every Paseo theme works.`);
      for (const m of text.matchAll(/\b(?:color|backgroundColor|borderColor|tintColor)\s*:\s*(["'])(white|black|red|green|blue|gray|grey|yellow|orange|purple)\1/gi))
        report("warn", file, lineOf(text, m.index), `Hardcoded color "${m[2]}" — use theme.colors.*.`);
    }
  }
}

// usage sources need 0.11
if (usesUsageSource && manifest?.requirements?.paseo) {
  const min = minPaseoVersion(manifest.requirements.paseo);
  if (min && lt(min, [0, 11, 0]))
    report("warn", usesUsageSource[0], usesUsageSource[1], `registerUsageSource requires Paseo 0.11 but requirements.paseo allows ${manifest.requirements.paseo}.`);
}

// SVG icon rules
for (const [rel, file, line] of svgIcons) {
  const full = path.resolve(root, rel);
  if (!full.startsWith(root + path.sep)) {
    report("error", file, line, `Icon "${rel}" must resolve inside the plugin directory.`);
    continue;
  }
  if (!existsSync(full)) {
    report("error", file, line, `Icon file "${rel}" not found (path is relative to the plugin directory).`);
    continue;
  }
  const size = statSync(full).size;
  if (size > 64 * 1024) report("error", full, null, `SVG icon is ${size} bytes; limit is 64 KiB.`);
  const svg = read(full);
  const bad = [
    [/<script\b/i, "<script>"],
    [/<style\b/i, "<style>"],
    [/<foreignObject\b/i, "<foreignObject>"],
    [/\son[a-z]+\s*=/i, "event-handler attribute"],
    [/javascript:/i, "javascript: URL"],
    [/\b(?:xlink:)?href\s*=\s*["'](?!#)/i, "external href"],
  ];
  for (const [re, what] of bad) if (re.test(svg)) report("error", full, null, `SVG icon contains ${what}; icons must be self-contained.`);
}

// tsconfig
const tsconfigPath = path.join(root, "tsconfig.json");
if (existsSync(tsconfigPath)) {
  const ts = stripComments(read(tsconfigPath));
  if (/"lib"\s*:\s*\[[^\]]*"dom(?:\.[a-z.]+)?"/i.test(ts))
    report("error", tsconfigPath, null, 'Remove "DOM" from compilerOptions.lib; the scaffold omits it so DOM globals fail typecheck.');
}

// package.json
const pkgPath = path.join(root, "package.json");
if (existsSync(pkgPath)) {
  let pkg = null;
  try {
    pkg = JSON.parse(read(pkgPath));
  } catch (e) {
    report("error", pkgPath, null, `package.json is not valid JSON: ${e.message}`);
  }
  if (pkg) {
    const hostProvided = ["@getpaseo/plugin", "react", "react-native", "zod", "@tanstack/react-query"];
    for (const dep of hostProvided) {
      if (pkg.dependencies?.[dep]) report("warn", pkgPath, null, `"${dep}" is host-provided; keep it in devDependencies.`);
    }
    if (Array.isArray(pkg.files)) {
      const need = ["paseo-plugin.json", ...["client/", "server/", "shared/"].filter((d) => existsSync(path.join(root, d)))];
      for (const n of need) {
        if (!pkg.files.some((f) => f.replace(/\/?$/, "/") === n || f === n || f === n.replace(/\/$/, "")))
          report("warn", pkgPath, null, `package.json "files" does not include "${n}" — it would be missing from the npm package.`);
      }
      for (const [rel] of svgIcons) {
        if (!pkg.files.some((f) => rel === f || rel.startsWith(f.replace(/\/?$/, "/"))))
          report("warn", pkgPath, null, `Icon "${rel}" is not listed in package.json "files".`);
      }
    }
    const runtimeDeps = Object.keys(pkg.dependencies ?? {}).filter((d) => !hostProvided.includes(d));
    if (runtimeDeps.length && !manifest?.build)
      report("info", pkgPath, null, `Runtime dependencies (${runtimeDeps.join(", ")}): npm installs handle them; for Git/GitHub sources commit package-lock.json and add "build": [["npm","ci","--omit=dev"]].`);
  }
}

// ---------- output ----------

const order = { error: 0, warn: 1, info: 2 };
findings.sort((a, b) => order[a.level] - order[b.level] || String(a.file).localeCompare(String(b.file)) || (a.line ?? 0) - (b.line ?? 0));
const errors = findings.filter((f) => f.level === "error").length;
const warnings = findings.filter((f) => f.level === "warn").length;

if (asJson) {
  console.log(JSON.stringify({ root, errors, warnings, findings }, null, 2));
} else {
  console.log(`Paseo plugin lint: ${root}`);
  for (const f of findings) {
    const loc = f.file ? `${f.file}${f.line ? `:${f.line}` : ""}` : "(plugin)";
    console.log(`${f.level.toUpperCase().padEnd(5)}  ${loc}  ${f.message}`);
  }
  console.log(`\n${errors} error(s), ${warnings} warning(s), ${findings.length - errors - warnings} note(s). Next: npm run typecheck && paseo plugin reload <id>`);
}
process.exit(errors ? 1 : 0);
