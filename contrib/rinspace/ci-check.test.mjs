// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import {mkdtempSync, writeFileSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {test} from 'node:test';

const script = new URL('./ci-check.sh', import.meta.url).pathname;
const commands = {
  'models-control': ['test -p 1 -vet=off ./models/rinauth ./models/rincontrol ./services/rincontrol'],
  authentication: ['test -p 1 -vet=off ./services/auth'],
  repository: ["test -p 1 -vet=off ./services/repository -run Rinspace|Publication"],
  'editor-catalog': [
    'test -p 1 ./models/db -run ^TestRinspaceCatalogPostgresIndexMetadata$',
    'test -p 1 ./modules/rinspacecatalog',
    'test -p 1 -tags sqlite,sqlite_unlock_notify ./models/repo -run ^TestRinspaceEditorProject(SQLIsBounded|Options)$',
    'test -p 1 -tags sqlite,sqlite_unlock_notify ./routers/api/v1/user -run ^TestRinspaceEditorCatalog',
    'test -c -p 1 -tags sqlite,sqlite_unlock_notify,rinspace_catalog_postgres -o /tmp/editor-catalog-postgres.test ./models/repo',
    'test -c -p 1 -tags sqlite,sqlite_unlock_notify,rinspace_catalog_postgres -o /tmp/editor-catalog-api.test ./tests/integration',
  ],
  migration: ['test -c -o /dev/null ./models/migrations/v1_27'],
};

function probe(mode, failure = 0, failureTool = 'go') {
  const directory = mkdtempSync(join(tmpdir(), 'rinspace-ci-contract-'));
  const log = join(directory, 'commands.jsonl');
  const stub = `#!${process.execPath}\nconst fs=require('node:fs'),tool=require('node:path').basename(process.argv[1]);fs.appendFileSync(process.env.CI_PROBE_LOG,JSON.stringify({tool,args:process.argv.slice(2),gomaxprocs:process.env.GOMAXPROCS,gomemlimit:process.env.GOMEMLIMIT})+'\\n');if(tool===process.env.CI_PROBE_TOOL)process.exit(Number(process.env.CI_PROBE_FAILURE));\n`;
  for (const name of ['go', 'git', 'gofmt', 'node']) writeFileSync(join(directory, name), stub, {mode: 0o700});
  writeFileSync(log, '');
  try {
    const result = spawnSync('/bin/bash', [script, mode], {encoding: 'utf8', timeout: 10000, env: {PATH: `${directory}:/usr/bin:/bin`, TMPDIR: directory, CI_PROBE_LOG: log, CI_PROBE_FAILURE: String(failure), CI_PROBE_TOOL: failureTool}});
    assert.ifError(result.error);
    return {status: result.status, calls: readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)};
  } finally {
    rmSync(directory, {recursive: true, force: true});
  }
}

for (const [mode, expected] of Object.entries(commands)) {
  test(`${mode} preserves all original Go commands and resource settings`, () => {
    const result = probe(mode);
    assert.equal(result.status, 0);
    assert.deepEqual(result.calls.map(call => call.args.join(' ')), expected);
    assert.ok(result.calls.every(call => call.tool === 'go' && call.gomaxprocs === '1' && call.gomemlimit === '2GiB'));
  });
}
test('formatting retains whitespace and all tracked Go file checks', () => {
  const result = probe('formatting');
  assert.equal(result.status, 0);
  assert.deepEqual(result.calls.map(call => [call.tool, ...call.args]), [['git', 'diff', '--check'], ['git', 'ls-files', '-z', '*.go'], ['gofmt', '-l'], ['node', '--test', 'contrib/rinspace/ci-check.test.mjs', 'contrib/rinspace/github-checks.test.mjs', 'contrib/rinspace/native-run.test.mjs']]);
});
test('reporter regression failures fail the shared formatting gate', () => {
  assert.equal(probe('formatting', 23, 'node').status, 23);
});
test('a failed subcommand stops the batch instead of reporting success', () => {
  const result = probe('editor-catalog', 23);
  assert.equal(result.status, 23);
  assert.equal(result.calls.length, 1);
});
test('unknown checks are rejected without executing anything', () => {
  const result = probe('focused-go; true');
  assert.equal(result.status, 64);
  assert.deepEqual(result.calls, []);
});
test('Actions uses the same six entry points and retains required check names', () => {
  const workflow = readFileSync(new URL('../../.github/workflows/rinspace-fork-checks.yml', import.meta.url), 'utf8');
  const modes = [...workflow.matchAll(/command: bash contrib\/rinspace\/ci-check.sh ([a-z-]+)/g)].map(match => match[1]);
  assert.deepEqual(modes, ['formatting', 'models-control', 'authentication', 'repository', 'editor-catalog', 'migration']);
  assert.match(workflow, /^  focused-go:/m);
  assert.match(workflow, /^  dockerfile-check:/m);
  assert.match(workflow, /docker buildx build --check --platform linux\/amd64 \./);
  assert.match(workflow, /docker buildx build --check --platform linux\/amd64 --file Dockerfile.rootless \./);
});
