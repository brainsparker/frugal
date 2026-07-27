#!/usr/bin/env node
// frugal-mcp — thin Node wrapper that fetches the matching Go binary
// from the GitHub release on first run, verifies its sha256 against the
// release's SHA256SUMS, caches it under ~/.cache/frugal-mcp/<version>/,
// and execs it with the caller's argv.
//
// Why a runtime download instead of bundling binaries in the npm tarball
// (esbuild-style optionalDependencies)? Simpler to publish (one package,
// not five), works with `--ignore-scripts` (no postinstall), and the
// download is ~15 MB cached forever. The optionalDependencies layout is
// a drop-in upgrade later if the audience grows past the convenience
// audience this wrapper targets.

import { spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createWriteStream, existsSync, mkdirSync, chmodSync, renameSync, unlinkSync, readFileSync, readdirSync, statSync } from "node:fs";
import { homedir, platform, arch } from "node:os";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { get } from "node:https";

const here = dirname(fileURLToPath(import.meta.url));
const pkg = JSON.parse(readFileSync(join(here, "..", "package.json"), "utf8"));

const VERSION = pkg.version;
const REPO = "brainsparker/frugal";

function targetTriple() {
  const os = platform();
  const cpu = arch();
  let goos;
  let goarch;
  switch (os) {
    case "darwin":
      goos = "darwin";
      break;
    case "linux":
      goos = "linux";
      break;
    default:
      // win32 is the other path most agent installs touch — Frugal has
      // no Windows release artifacts yet, so we fail fast with a useful
      // message rather than 404-ing on the download.
      throw new Error(`frugal-mcp: unsupported OS ${os} (darwin and linux only for now)`);
  }
  switch (cpu) {
    case "arm64":
      goarch = "arm64";
      break;
    case "x64":
      goarch = "amd64";
      break;
    default:
      throw new Error(`frugal-mcp: unsupported CPU ${cpu} (arm64 and x64 only)`);
  }
  return `${goos}-${goarch}`;
}

function cacheBinaryPath() {
  const cacheRoot = process.env.XDG_CACHE_HOME || join(homedir(), ".cache");
  return join(cacheRoot, "frugal-mcp", VERSION, `frugal-${targetTriple()}`);
}

// Follow up to 5 redirects (GitHub release assets bounce through
// objects.githubusercontent.com) and hand the final 200 response to
// `consume(res, resolve, reject)`, which owns settling the promise.
function fetchFollowingRedirects(url, consume) {
  return new Promise((resolve, reject) => {
    const fetchOnce = (u, hops = 0) => {
      if (hops > 5) return reject(new Error("too many redirects"));
      const req = get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          fetchOnce(new URL(res.headers.location, u).toString(), hops + 1);
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`download ${u} → HTTP ${res.statusCode}`));
          return;
        }
        consume(res, resolve, reject);
      });
      req.on("error", reject);
    };
    fetchOnce(url);
  });
}

function downloadToFile(url, dest) {
  return fetchFollowingRedirects(url, (res, resolve, reject) => {
    const out = createWriteStream(dest);
    const fail = (err) => {
      out.destroy();
      try { unlinkSync(dest); } catch {}
      reject(err);
    };
    // A socket reset mid-body surfaces on the response stream, not the
    // request — without these handlers the promise hangs forever on a
    // torn temp file.
    res.on("error", fail);
    res.on("aborted", () => fail(new Error(`download ${url} aborted mid-body`)));
    out.on("error", fail);
    out.on("finish", () => out.close(resolve));
    res.pipe(out);
  });
}

function downloadText(url) {
  return fetchFollowingRedirects(url, (res, resolve, reject) => {
    const chunks = [];
    res.on("data", (c) => chunks.push(c));
    res.on("error", reject);
    res.on("aborted", () => reject(new Error(`download ${url} aborted mid-body`)));
    res.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
  });
}

// SHA256SUMS lines are `<hex>  <name>` (shasum -a 256 output; a leading
// `*` on the name means binary mode — accept both).
function expectedSha256(sums, assetName) {
  for (const line of sums.split("\n")) {
    const m = line.trim().match(/^([0-9a-fA-F]{64})\s+\*?(.+)$/);
    if (m && m[2] === assetName) return m[1].toLowerCase();
  }
  throw new Error(`SHA256SUMS has no entry for ${assetName}`);
}

function fileSha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

async function ensureBinary() {
  const binPath = cacheBinaryPath();
  if (existsSync(binPath)) return binPath;
  const triple = targetTriple();
  const asset = `frugal-${triple}`;
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  mkdirSync(dirname(binPath), { recursive: true });
  reapStalePartials(binPath);
  // Download to a process-unique sibling temp path. A sibling temp keeps
  // a partial download from a crashed process out of the cache; the
  // pid+random suffix keeps two clients cold-starting at once (Claude
  // Desktop and Cursor after `frugal mcp install` and a reboot) from
  // truncating each other's in-flight download. Each process verifies its
  // own temp, then atomically renames onto the final path — a concurrent
  // winner renaming identical verified bytes first is harmless.
  const tmpPath = `${binPath}.partial.${process.pid}.${randomBytes(4).toString("hex")}`;
  process.stderr.write(`frugal-mcp: fetching v${VERSION} for ${triple} (~15 MB, cached after)…\n`);
  // Verify against the SHA256SUMS from the same release. This catches
  // torn downloads and swapped assets; it is not a signature check —
  // cosign verification of the release is the curl installer's job.
  const sums = await downloadText(`${base}/SHA256SUMS`);
  const want = expectedSha256(sums, asset);
  try {
    await downloadToFile(`${base}/${asset}`, tmpPath);
    const got = fileSha256(tmpPath);
    if (got !== want) {
      throw new Error(`sha256 mismatch for ${asset}: got ${got}, want ${want} — refusing to cache or run it`);
    }
    chmodSync(tmpPath, 0o755);
    renameSync(tmpPath, binPath);
  } catch (err) {
    // Every failure path drops the temp so crashed runs don't litter the
    // cache dir (unlink is a no-op when downloadToFile already cleaned up).
    try { unlinkSync(tmpPath); } catch {}
    throw err;
  }
  return binPath;
}

// reapStalePartials removes leftover `.partial.*` temp files from
// crashed or SIGKILLed downloads next to binPath. The pid+random temp
// names that protect concurrent cold-starts also mean nobody else ever
// renames or truncates an orphan, so without this a client retry loop on
// a slow network strands ~15 MB per attempt. Age-gated to an hour so a
// concurrent in-flight download is never reaped.
function reapStalePartials(binPath) {
  const dir = dirname(binPath);
  const prefix = `${basename(binPath)}.partial.`;
  let names;
  try {
    names = readdirSync(dir);
  } catch {
    return;
  }
  const cutoff = Date.now() - 60 * 60 * 1000;
  for (const name of names) {
    if (!name.startsWith(prefix)) continue;
    const p = join(dir, name);
    try {
      if (statSync(p).mtimeMs < cutoff) unlinkSync(p);
    } catch {
      // Already gone or unreadable — either way, not our problem.
    }
  }
}

async function main() {
  // MCP clients always pass argv (`mcp serve`); a bare invocation, help,
  // or version flag is a human (or script) exploring, so answer locally —
  // these must work offline, not trigger a ~15 MB fetch.
  const args = process.argv.slice(2);
  if (args[0] === "--version" || args[0] === "-v" || args[0] === "version") {
    process.stdout.write(`frugal-mcp v${VERSION} (npm wrapper; the Go binary is fetched on first real command)\n`);
    process.exit(0);
  }
  if (args.length === 0 || args[0] === "--help" || args[0] === "-h" || args[0] === "help") {
    const usage =
      "frugal-mcp — npm wrapper that runs the frugal Go binary\n" +
      "Usage:\n" +
      "  frugal-mcp mcp serve     run the MCP server over stdio (what agent clients invoke)\n" +
      "  frugal-mcp mcp install   wire Frugal into Claude Desktop, Cursor, AnythingLLM, or Claude Code\n" +
      "  frugal-mcp stats         show this month's tool-call savings receipt\n" +
      "The first real command downloads and checksum-verifies the binary (~15 MB, cached).\n" +
      "Full CLI reference: https://github.com/brainsparker/frugal#readme\n";
    if (args.length === 0) {
      process.stderr.write("frugal-mcp: no command given.\n" + usage);
      process.exit(2);
    }
    process.stdout.write(usage);
    process.exit(0);
  }
  let binPath;
  try {
    binPath = await ensureBinary();
  } catch (err) {
    process.stderr.write(`frugal-mcp: ${err.message}\n`);
    process.exit(1);
  }
  const res = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
  if (res.error) {
    process.stderr.write(`frugal-mcp: exec failed: ${res.error.message}\n`);
    process.exit(1);
  }
  process.exit(res.status ?? 1);
}

main();
