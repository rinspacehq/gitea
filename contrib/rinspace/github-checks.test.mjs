// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// All responses, credentials and executor results below are synthetic/offline.
import assert from 'node:assert/strict';
import {generateKeyPairSync, verify} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {appJwt, checkPlan, GithubChecks} from './github-checks.mjs';

const {privateKey, publicKey} = generateKeyPairSync('rsa', {modulusLength: 2048});
const pem = privateKey.export({type: 'pkcs8', format: 'pem'});
const now = Date.parse('2026-10-16T02:00:00Z');
const sha = 'a'.repeat(40);
const config = {appId: 123, installationId: 456, appSlug: 'synthetic-rinspace-ci', privateKey: pem};
const permissions = {checks: 'write', contents: 'read', pull_requests: 'read', metadata: 'read'};
const goodResult = () => ({exitCode: 0, signal: null, timedOut: false, cancelled: false});

function fixture(overrides = {}) {
  const calls = [];
  const state = {pullReads: 0, branchReads: 0, check: null, tokenCount: 0};
  const fetch = async (url, options) => {
    assert.ok(url.startsWith('https://api.github.com/'));
    assert.equal(options.redirect, 'error');
    assert.equal(options.headers['X-GitHub-Api-Version'], '2022-11-28');
    const path = new URL(url).pathname;
    const body = options.body ? JSON.parse(options.body) : null;
    const record = {path, method: options.method, body, authorization: options.headers.Authorization};
    calls.push(record);
    let value;
    if (path === '/app') value = {id: 123, slug: config.appSlug, owner: {login: 'rinspacehq'}, permissions, ...overrides.app};
    else if (path === '/repos/rinspacehq/gitea/installation') value = {id: 456, app_id: 123, account: {login: 'rinspacehq'}, repository_selection: 'selected', suspended_at: null, permissions, ...overrides.installation};
    else if (path === '/app/installations/456/access_tokens') {
      state.tokenCount++;
      assert.deepEqual(body, {repositories: ['gitea'], permissions});
      value = {token: 'synthetic-installation-token', permissions, expires_at: new Date(now + 3600000).toISOString(), ...overrides.token};
    } else if (path === '/installation/repositories') value = {total_count: 1, repositories: [{full_name: 'rinspacehq/gitea'}], ...overrides.repositories};
    else if (path === '/repos/rinspacehq/gitea/branches/rinspace-1.27') {
      state.branchReads++;
      value = {name: 'rinspace-1.27', protected: true, commit: {sha}, ...overrides.branch};
      if (overrides.maintenanceMoved && state.branchReads > 1) value.commit.sha = 'b'.repeat(40);
    } else if (path === '/repos/rinspacehq/gitea/pulls/7') {
      state.pullReads++;
      value = {state: 'open', head: {sha, repo: {full_name: 'rinspacehq/gitea'}}, base: {ref: 'rinspace-1.27', repo: {full_name: 'rinspacehq/gitea'}}, ...overrides.pull};
      if (overrides.moved && state.pullReads > 1) value.head.sha = 'b'.repeat(40);
    } else if (path === '/repos/rinspacehq/gitea/check-runs' && options.method === 'POST') {
      state.check = {id: 789, app: {id: 123}, ...body};
      value = {...state.check, ...overrides.created};
    } else if (path === '/repos/rinspacehq/gitea/check-runs/789' && options.method === 'GET') value = {...state.check, ...overrides.current};
    else if (path === '/repos/rinspacehq/gitea/check-runs/789' && options.method === 'PATCH') {
      state.check = {...state.check, ...body};
      value = {...state.check, ...overrides.finished};
    } else throw Error('Unexpected offline API request');
    if (overrides.networkError === path) throw Error('synthetic-installation-token must not escape');
    return {ok: overrides.httpError !== path, status: overrides.httpError === path ? 403 : 200, json: async () => value};
  };
  const client = new GithubChecks(config, {fetch, now: () => now});
  return {client, calls, state};
}

const request = execute => ({pullNumber: 7, sha, name: 'focused-go', execute});
const updates = calls => calls.filter(call => call.method === 'PATCH');

test('JWT uses RS256, backdated iat and a bounded expiry', () => {
  const jwt = appJwt(123, pem, now);
  const [header, payload, signature] = jwt.split('.');
  assert.deepEqual(JSON.parse(Buffer.from(header, 'base64url')), {alg: 'RS256', typ: 'JWT'});
  assert.deepEqual(JSON.parse(Buffer.from(payload, 'base64url')), {iss: '123', iat: now / 1000 - 60, exp: now / 1000 + 540});
  assert.ok(verify('RSA-SHA256', Buffer.from(`${header}.${payload}`), publicKey, Buffer.from(signature, 'base64url')));
});

test('all six Go steps complete serially and only minimal data reaches the executor', async () => {
  const {client, calls} = fixture();
  let completed = 0;
  const result = await client.runPullCheck(request(async step => {
    assert.deepEqual(Object.keys(step).sort(), ['argv', 'name', 'sha', 'step']);
    assert.equal(step.sha, sha);
    assert.equal(step.step, completed++);
    assert.ok(Object.isFrozen(step) && Object.isFrozen(step.argv));
    return goodResult();
  }));
  assert.deepEqual(result, {id: 789, sha, name: 'focused-go', conclusion: 'success', completed: 6, total: 6});
  assert.equal(updates(calls).length, 1);
  assert.equal(updates(calls)[0].body.conclusion, 'success');
  assert.equal(calls.filter(call => call.path.endsWith('/pulls/7')).length, 2);
  assert.ok(calls.filter(call => call.path.includes('/check-runs')).every(call => call.authorization === 'Bearer synthetic-installation-token'));
});

test('Docker requires both exact BuildKit check commands', async () => {
  const {client} = fixture();
  const executed = [];
  const result = await client.runPullCheck({pullNumber: 7, sha, name: 'dockerfile-check', execute: async step => {executed.push(step.argv); return goodResult();}});
  assert.equal(result.total, 2);
  assert.deepEqual(executed, [
    ['docker', 'buildx', 'build', '--check', '--platform', 'linux/amd64', '.'],
    ['docker', 'buildx', 'build', '--check', '--platform', 'linux/amd64', '--file', 'Dockerfile.rootless', '.'],
  ]);
});

test('maintenance checks run the exact newly rebased SHA, not inherited PR results', async () => {
  const {client, state} = fixture();
  let executed = 0;
  const result = await client.runMaintenanceCheck({sha, name: 'focused-go', execute: async step => {
    assert.equal(step.sha, sha); executed++; return goodResult();
  }});
  assert.equal(result.conclusion, 'success'); assert.equal(executed, 6);
  assert.equal(state.branchReads, 2); assert.equal(state.pullReads, 0);
});
for (const branch of [{protected: false}, {name: 'main'}, {commit: {sha: 'b'.repeat(40)}}]) {
  test(`maintenance checks refuse unprotected or unrelated source ${JSON.stringify(branch)}`, async () => {
    const {client, state} = fixture({branch});
    await assert.rejects(client.runMaintenanceCheck({sha, name: 'focused-go', execute: async () => assert.fail('must not execute')}));
    assert.equal(state.check, null);
  });
}
test('maintenance head movement cancels a completed matrix', async () => {
  const {client} = fixture({maintenanceMoved: true});
  assert.equal((await client.runMaintenanceCheck({sha, name: 'dockerfile-check', execute: async () => goodResult()})).conclusion, 'cancelled');
});
test('neither PR nor maintenance check endpoints can substitute the native publisher', async () => {
  const {client, state} = fixture();
  await assert.rejects(client.runPullCheck({...request(async () => goodResult()), name: 'native-release'}));
  await assert.rejects(client.runMaintenanceCheck({sha, name: 'native-release', execute: async () => goodResult()}));
  assert.equal(state.check, null); assert.equal(state.tokenCount, 0);
});

for (const [name, result, expected] of [
  ['nonzero exit', {...goodResult(), exitCode: 2}, 'failure'],
  ['signal death', {...goodResult(), signal: 'SIGKILL'}, 'failure'],
  ['timeout', {...goodResult(), timedOut: true}, 'timed_out'],
  ['cancelled', {...goodResult(), cancelled: true}, 'cancelled'],
  ['missing receipt', undefined, 'failure'],
  ['incomplete receipt', {exitCode: 0}, 'failure'],
  ['wrong exit type', {...goodResult(), exitCode: '0'}, 'failure'],
]) {
  test(`${name} cannot turn green or execute remaining steps`, async () => {
    const {client, calls} = fixture();
    let executed = 0;
    const finished = await client.runPullCheck(request(async () => {executed++; return result;}));
    assert.equal(finished.conclusion, expected);
    assert.equal(finished.completed, 0);
    assert.equal(executed, 1);
    assert.equal(updates(calls)[0].body.conclusion, expected);
  });
}

test('executor exception becomes failure without publishing its message', async () => {
  const {client, calls} = fixture();
  assert.equal((await client.runPullCheck(request(async () => {throw Error('synthetic-secret-from-worker');}))).conclusion, 'failure');
  assert.ok(!JSON.stringify(updates(calls)).includes('synthetic-secret-from-worker'));
});

test('head movement after passing tests cancels the old check', async () => {
  const {client, calls} = fixture({moved: true});
  assert.equal((await client.runPullCheck(request(async () => goodResult()))).conclusion, 'cancelled');
  assert.equal(updates(calls)[0].body.conclusion, 'cancelled');
});

for (const [name, overrides] of [
  ['wrong App owner', {app: {owner: {login: 'someone-else'}}}],
  ['wrong App ID', {app: {id: 999}}],
  ['wrong App slug', {app: {slug: 'another-app'}}],
  ['excess App permissions', {app: {permissions: {...permissions, administration: 'write'}}}],
  ['wrong installation ID', {installation: {id: 999}}],
  ['wrong installation App', {installation: {app_id: 999}}],
  ['wrong installation owner', {installation: {account: {login: 'someone-else'}}}],
  ['all-repository installation', {installation: {repository_selection: 'all'}}],
  ['suspended installation', {installation: {suspended_at: '2026-10-15T00:00:00Z'}}],
  ['excess installation permissions', {installation: {permissions: {...permissions, contents: 'write'}}}],
  ['expired token', {token: {expires_at: new Date(now - 1).toISOString()}}],
  ['excess token permissions', {token: {permissions: {...permissions, contents: 'write'}}}],
  ['wrong repository', {repositories: {repositories: [{full_name: 'someone-else/gitea'}]}}],
  ['broad token', {repositories: {total_count: 2}}],
  ['closed PR', {pull: {state: 'closed'}}],
  ['external fork', {pull: {head: {sha, repo: {full_name: 'someone-else/gitea'}}}}],
  ['stale head', {pull: {head: {sha: 'b'.repeat(40), repo: {full_name: 'rinspacehq/gitea'}}}}],
  ['wrong base line', {pull: {base: {ref: 'main', repo: {full_name: 'rinspacehq/gitea'}}}}],
]) {
  test(`${name} rejects before check creation or execution`, async () => {
    const {client, calls} = fixture(overrides);
    let executed = false;
    await assert.rejects(client.runPullCheck(request(async () => {executed = true; return goodResult();})));
    assert.equal(executed, false);
    assert.ok(!calls.some(call => call.path.endsWith('/check-runs')));
  });
}

for (const [name, current] of [
  ['another check ID', {id: 999}],
  ['another App', {app: {id: 999}}],
  ['another SHA', {head_sha: 'b'.repeat(40)}],
  ['another context', {name: 'unrelated'}],
  ['already completed', {status: 'completed'}],
]) {
  test(`refuses to overwrite ${name}`, async () => {
    const {client, calls} = fixture({current});
    await assert.rejects(client.runPullCheck(request(async () => goodResult())));
    assert.deepEqual(updates(calls), []);
  });
}

test('network failures cannot disclose a token or create a successful check', async () => {
  const {client, calls} = fixture({networkError: '/installation/repositories'});
  await assert.rejects(client.runPullCheck(request(async () => goodResult())), error => !error.message.includes('synthetic-installation-token'));
  assert.deepEqual(updates(calls), []);
});

test('invalid SHA, pull number and check context make no API calls', async () => {
  const {client, calls} = fixture();
  for (const invalid of [{sha: 'main'}, {pullNumber: -1}, {name: 'constructor'}, {name: '../focused-go'}]) {
    await assert.rejects(client.runPullCheck({...request(async () => goodResult()), ...invalid}));
  }
  assert.deepEqual(calls, []);
});

test('check recipes are immutable and cannot be widened by a caller', () => {
  assert.throws(() => checkPlan('focused-go').push(['true']));
  assert.throws(() => {checkPlan('dockerfile-check')[0][0] = 'true';});
});

test('independent plans contain every Actions entry without a second command variant', () => {
  const workflow = readFileSync(new URL('../../.github/workflows/rinspace-fork-checks.yml', import.meta.url), 'utf8');
  const go = [...workflow.matchAll(/command: (bash contrib\/rinspace\/ci-check.sh [a-z-]+)/g)].map(match => match[1]);
  const docker = [...workflow.matchAll(/run: (docker buildx build[^\n]+)/g)].map(match => match[1]);
  assert.deepEqual(checkPlan('focused-go').map(argv => argv.join(' ')), go);
  assert.deepEqual(checkPlan('dockerfile-check').map(argv => argv.join(' ')), docker);
});

test('a late failure records only completed steps and never skips ahead', async () => {
  const {client} = fixture();
  let invoked = 0;
  const finished = await client.runPullCheck(request(async () => (++invoked === 2 ? {...goodResult(), exitCode: 1} : goodResult())));
  assert.equal(finished.conclusion, 'failure');
  assert.equal(finished.completed, 1);
  assert.equal(invoked, 2);
});

test('HTTP rejection cannot create a successful check', async () => {
  const {client, calls} = fixture({httpError: '/repos/rinspacehq/gitea/pulls/7'});
  await assert.rejects(client.runPullCheck(request(async () => goodResult())), /GitHub rejected GET/);
  assert.deepEqual(updates(calls), []);
});

test('unexpected create response is rejected before executing candidate code', async () => {
  const {client, calls} = fixture({created: {app: {id: 999}}});
  let invoked = false;
  await assert.rejects(client.runPullCheck(request(async () => {invoked = true; return goodResult();})));
  assert.equal(invoked, false);
  assert.deepEqual(updates(calls), []);
});

test('durable events bracket API mutations with the exact external ID and check ID', async () => {
  const {client, calls} = fixture();
  const events = [];
  await client.runPullCheck({...request(async () => goodResult()), record: async event => {
    if (event.stage === 'check-creating') assert.ok(!calls.some(call => call.method === 'POST' && call.path.endsWith('/check-runs')));
    if (event.stage === 'check-completing') assert.equal(updates(calls).length, 0);
    events.push(event);
  }});
  assert.deepEqual(events.map(event => event.stage), ['check-creating', 'check-created', 'check-completing', 'check-completed']);
  assert.equal(events[0].externalId, calls.find(call => call.path.endsWith('/check-runs')).body.external_id);
  assert.ok(events.slice(1).every(event => event.checkId === 789));
  assert.ok(!JSON.stringify(events).includes('synthetic-installation-token'));
});

for (const stage of ['check-creating', 'check-completing']) {
  test(`durable write failure at ${stage} prevents its API mutation`, async () => {
    const {client, calls} = fixture();
    await assert.rejects(client.runPullCheck({...request(async () => goodResult()), record: async event => {
      if (event.stage === stage) throw Error('synthetic disk write failure');
    }}), /disk write failure/);
    assert.equal(updates(calls).length, 0);
    if (stage === 'check-creating') assert.ok(!calls.some(call => call.path.endsWith('/check-runs')));
  });
}
