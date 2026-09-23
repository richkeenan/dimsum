#!/usr/bin/env node
// Build provenance and project/dependency license notices.
import { sourceInputs } from './source-inputs.mjs';
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
if (process.argv.length !== 3)
  throw new Error("Usage: artifact-metadata.mjs OUTPUT_DIRECTORY");
const out = path.resolve(process.argv[2]);
const assets = path.join(root, "internal/webassets/dist");
if (
  !existsSync(path.join(assets, "index.html")) ||
  !existsSync(path.join(root, "web/node_modules"))
) {
  throw new Error(
    "Build the frontend with scripts/build-web.sh before collecting artifact metadata",
  );
}
mkdirSync(out, { recursive: true });
const command = (...args) =>
  execFileSync(args[0], args.slice(1), {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  }).trim();
const digest = (file) =>
  createHash("sha256").update(readFileSync(file)).digest("hex");
const writeJSON = (name, value) =>
  writeFileSync(path.join(out, name), JSON.stringify(value, null, 2) + "\n");
function hashes(directory, relativeTo) {
  const result = {};
  function walk(folder) {
    for (const entry of readdirSync(folder, { withFileTypes: true }).sort(
      (a, b) => a.name.localeCompare(b.name, "en"),
    )) {
      const file = path.join(folder, entry.name);
      if (entry.isDirectory()) walk(file);
      else if (entry.isFile())
        result[path.relative(relativeTo, file).split(path.sep).join("/")] =
          digest(file);
    }
  }
  walk(directory);
  return result;
}
// Fetch the full module graph before hashing inputs: this can add missing
// checksums to go.sum and must be reflected in both hashes and dirty state.
command("go", "mod", "download", "all");
const source = sourceInputs(root);
const metadata = {
  schema: 1,
  commit: source.commit,
  commit_timestamp: source.commit_timestamp,
  dirty: source.dirty,
  go: command("go", "version"),
  node: process.version,
  npm: command("npm", "--version"),
  build_configuration: ".goreleaser.yaml",
  binary_metadata:
    "main.version/commit/date injected by GoReleaser; date is source commit time, not wall clock",
  inputs: Object.fromEntries(
    ["go.mod", "go.sum", "web/package-lock.json", ".goreleaser.yaml"].map(
      (name) => [name, digest(path.join(root, name))],
    ),
  ),
  project_license: "MIT",
  assets: hashes(assets, assets),
  source_files: source.files,
};
writeJSON("build.json", metadata);
for (const [sourceFile, destination] of [["LICENSE", "LICENSE"], ["web/THIRD_PARTY.md", "THIRD_PARTY.md"]]) {
  copyFileSync(path.join(root, sourceFile), path.join(out, destination));
}
writeFileSync(
  path.join(out, "go-modules.txt"),
  command("go", "list", "-m", "all") + "\n",
);
copyFileSync(
  path.join(root, "web/package-lock.json"),
  path.join(out, "web-package-lock.json"),
);
const notices = path.join(out, "licenses");
// Discard stale notices from prior dependency graphs, including old .go notice
// filenames that would otherwise be discovered by `go test ./...`.
rmSync(notices, { recursive: true, force: true });
mkdirSync(notices, { recursive: true });
const inventory = [],
  missing = [];
function collect(name, directory, licenseHint = null) {
  const candidates = readdirSync(directory, { withFileTypes: true })
    .filter(
      (entry) =>
        entry.isFile() &&
        /^(LICENSE|COPYING|NOTICE|COPYRIGHT|AUTHORS)/i.test(entry.name),
    )
    .map((entry) => entry.name)
    .sort();
  inventory.push({
    dependency: name,
    license_metadata: licenseHint,
    notice_files: candidates.map((file) => file + ".txt"),
  });
  if (!candidates.length) missing.push(name);
  const target = path.join(
    notices,
    name.replaceAll("/", "_").replaceAll("@", "_"),
  );
  mkdirSync(target, { recursive: true });
  for (const file of candidates) {
    const destination = path.join(target, file + ".txt");
    // Go's module cache contains read-only files; replace the previous copy
    // rather than trying to overwrite its preserved read-only permissions.
    rmSync(destination, { force: true });
    copyFileSync(path.join(directory, file), destination);
  }
}
// Include the entire module graph conservatively so cross-platform builds also
// carry notices for dependencies hidden by host-specific build constraints.
const template =
  "{{if not .Main}}{{if .Replace}}{{with .Replace}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{else}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}";
const modules = new Set(
  command("go", "list", "-m", "-f", template, "all")
    .split("\n")
    .filter(Boolean),
);
for (const line of modules) {
  const [name, version, directory] = line.split("\t");
  collect(`${name}@${version}`, directory);
}
collect("go-toolchain", command("go", "env", "GOROOT"));
const lock = JSON.parse(
  readFileSync(path.join(root, "web/package-lock.json"), "utf8"),
);
for (const [name, value] of Object.entries(lock.packages)) {
  if (name && existsSync(path.join(root, "web", name)))
    collect(
      `${name}@${value.version || "local"}`,
      path.join(root, "web", name),
      value.license ?? null,
    );
}
writeJSON("license-inventory.json", inventory);
writeFileSync(
  path.join(out, "NOTICES.txt"),
  "dimsum is licensed under the MIT License; see LICENSE.\nCopied component notices are in THIRD_PARTY.md.\nDependency license texts are copied verbatim. All Go modules and installed frontend build dependencies are included conservatively.\nPackages without standalone notice files (consult license-inventory.json):\n" +
    missing.map((name) => `- ${name}\n`).join(""),
);
