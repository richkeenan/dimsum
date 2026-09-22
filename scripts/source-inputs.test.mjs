import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { sourceInputs } from './source-inputs.mjs';

test('provenance excludes private and untracked files and reports modified inputs', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'dimsum-inputs-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', args, { cwd: root, stdio: 'pipe' });
  git('init');
  mkdirSync(path.join(root, 'internal'));
  writeFileSync(path.join(root, '.gitignore'), 'internal/private.txt\n');
  writeFileSync(path.join(root, 'internal/main.go'), 'package main\n');
  git('add', '.');
  git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-m', 'fixture');
  writeFileSync(path.join(root, 'internal/private.txt'), 'private');
  const clean = sourceInputs(root);
  assert.equal(clean.dirty, false);
  assert.deepEqual(Object.keys(clean.files).sort(), ['.gitignore', 'internal/main.go']);
  writeFileSync(path.join(root, 'internal/untracked.go'), 'package scratch\n');
  const untracked = sourceInputs(root);
  assert.equal(untracked.dirty, true);
  assert.equal(untracked.files['internal/untracked.go'], undefined);
  writeFileSync(path.join(root, 'internal/main.go'), 'package changed\n');
  assert.notEqual(sourceInputs(root).files['internal/main.go'], clean.files['internal/main.go']);
});

test('archive provenance refuses symlinks and excludes private directories', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'dimsum-archive-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(path.join(root, 'docs'));
  mkdirSync(path.join(root, 'web'));
  writeFileSync(path.join(root, 'docs/private.md'), 'private');
  writeFileSync(path.join(root, 'AGENTS.local.md'), 'private');
  writeFileSync(path.join(root, 'web/package.json'), '{}');
  const archive = sourceInputs(root);
  assert.equal(archive.dirty, 'unknown (source archive)');
  assert.deepEqual(Object.keys(archive.files), ['web/package.json']);
  symlinkSync('../AGENTS.local.md', path.join(root, 'web/leak.md'));
  assert.throws(() => sourceInputs(root), /regular file/);
});
