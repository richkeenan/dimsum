import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, lstatSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

// Source archives have no Git index. Admit only public source directories and
// omit generated output; callers must obtain archives from a trusted release.
const directories = new Set(['.github', 'api', 'bench', 'cmd', 'deploy', 'guides', 'internal', 'scripts', 'testdata', 'tests', 'web']);
const rootFiles = new Set(['.gitignore', '.goreleaser.yaml', 'AGENTS.md', 'CONTRIBUTING.md', 'LICENSE', 'README.md', 'SECURITY.md', 'go.mod', 'go.sum', 'install.sh']);
const excluded = new Set(['node_modules', 'dist', 'test-results', 'playwright-report', '__pycache__', '.vite', '.tanstack', '.git', 'docs', 'superpowers', '.superpowers']);

export function sourceInputs(root) {
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  const checkout = existsSync(path.join(root, '.git'));
  const names = [];
  if (checkout) {
    names.push(...git('ls-files', '-z').split('\0').filter(Boolean));
  } else {
    const walk = (relative) => {
      for (const entry of readdirSync(path.join(root, relative), { withFileTypes: true })) {
        if (excluded.has(entry.name) || entry.name === 'AGENTS.local.md') continue;
        const name = relative ? `${relative}/${entry.name}` : entry.name;
        if (!relative && !directories.has(entry.name) && !rootFiles.has(entry.name)) continue;
        if (entry.isDirectory()) walk(name);
        else names.push(name);
      }
    };
    walk('');
  }
  const files = {};
  for (const name of names.sort()) {
    const file = path.join(root, name);
    let info;
    try { info = lstatSync(file); } catch (error) {
      if (checkout && error.code === 'ENOENT') continue;
      throw error;
    }
    if (!info.isFile()) throw new Error(`Source input must be a regular file: ${name}`);
    files[name] = createHash('sha256').update(readFileSync(file)).digest('hex');
  }
  return {
    commit: checkout ? git('rev-parse', 'HEAD').trim() : process.env.SOURCE_COMMIT || 'unrecorded',
    commit_timestamp: checkout ? git('show', '-s', '--format=%ct', 'HEAD').trim() : process.env.SOURCE_DATE_EPOCH || 'unrecorded',
    dirty: checkout ? Boolean(git('status', '--porcelain', '--untracked-files=all').trim()) : 'unknown (source archive)',
    files,
  };
}
