// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Offline supervisor/API fixtures; not real GitHub checks or namespace proof.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {existsSync, lstatSync, mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {test} from 'node:test';
import {checkPlan} from './github-checks.mjs';
import {runNativePullCheck, runNativeMaintenanceCheck, systemdExecutor} from './native-run.mjs';

const sha = 'a'.repeat(40);
const name = 'focused-go';
const passed = () => ({exitCode: 0, signal: null, timedOut: false, cancelled: false});
function temporary(t) {
  const directory = mkdtempSync(join(tmpdir(), 'rinspace-native-test-'));
  t.after(() => rmSync(directory, {recursive: true, force: true}));
  return directory;
}

function reporter(action = async () => {}) {
  return {runPullCheck: async request => {
    await action(request);
    return {sha, name: request.name, conclusion: 'success'};
  }};
}

test('durable reservation precedes reporting and terminal success releases global lock', async t => {
  const directory = temporary(t);
  await runNativePullCheck({directory, pullNumber: 7, sha, name, execute: passed,
    reporter: reporter(async request => {
      assert.ok(existsSync(join(directory, 'active')));
      assert.equal(JSON.parse(readFileSync(join(directory, `${sha}-${name}.json`))).status, 'reserved');
      await request.record({stage: 'check-creating', externalId: 'synthetic-id'});
      await request.execute({sha, name, step: 0, argv: checkPlan(name)[0]});
    })});
  const state = JSON.parse(readFileSync(join(directory, `${sha}-${name}.json`)));
  assert.equal(state.status, 'completed');
  assert.ok(state.events.some(event => event.externalId === 'synthetic-id'));
  assert.equal(existsSync(join(directory, 'active')), false);
});

test('maintenance transport preserves durable supervision without inventing a pull number', async t => {
  const directory = temporary(t);
  await runNativeMaintenanceCheck({directory, sha, name, execute: passed,
    reporter: {runMaintenanceCheck: async request => {
      assert.equal(Object.hasOwn(request, 'pullNumber'), false);
      assert.ok(existsSync(join(directory, 'active')));
      await request.execute({sha, name, step: 0, argv: checkPlan(name)[0]});
      return {sha, name, conclusion: 'success'};
    }}});
  assert.equal(JSON.parse(readFileSync(join(directory, `${sha}-${name}.json`))).status, 'completed');
  assert.equal(existsSync(join(directory, 'active')), false);
  await assert.rejects(runNativeMaintenanceCheck({directory, sha, name, pullNumber: 7}), /impersonate/);
});

test('duplicate SHA/context refuses to replace its original evidence', async t => {
  const directory = temporary(t);
  const request = {directory, reporter: reporter(), pullNumber: 7, sha, name, execute: passed};
  await runNativePullCheck(request);
  const before = readFileSync(join(directory, `${sha}-${name}.json`), 'utf8');
  await assert.rejects(runNativePullCheck(request), /EEXIST/);
  assert.equal(readFileSync(join(directory, `${sha}-${name}.json`), 'utf8'), before);
  assert.ok(existsSync(join(directory, 'active')));
});

test('a live single-flight reservation prevents a second context from starting', async t => {
  const directory = temporary(t);
  await runNativePullCheck({directory, pullNumber: 7, sha, name, execute: passed,
    reporter: reporter(async () => {
      await assert.rejects(runNativePullCheck({directory, reporter: reporter(), pullNumber: 7, sha, name: 'dockerfile-check', execute: passed}), /EEXIST/);
    })});
});

test('ambiguous API response preserves lock and check identity', async t => {
  const directory = temporary(t);
  await assert.rejects(runNativePullCheck({directory, pullNumber: 7, sha, name, execute: passed,
    reporter: reporter(async request => {
      await request.record({stage: 'check-creating', externalId: 'synthetic-unknown-post'});
      throw Error('offline lost POST response');
    })}), /lost POST/);
  assert.ok(existsSync(join(directory, 'active')));
  const state = JSON.parse(readFileSync(join(directory, `${sha}-${name}.json`)));
  assert.equal(state.status, 'reserved');
  assert.equal(state.events.at(-1).externalId, 'synthetic-unknown-post');
});

test('executor uncertainty remains locked even if a reporter catches and marks failure', async t => {
  const directory = temporary(t);
  await assert.rejects(runNativePullCheck({directory, pullNumber: 7, sha, name,
    execute: async () => {throw Error('cleanup not confirmed');},
    reporter: reporter(async request => {
      await assert.rejects(request.execute({sha, name, step: 0, argv: checkPlan(name)[0]}));
    })}), /uncertain/);
  assert.ok(existsSync(join(directory, 'active')));
});

function supervisorFixture(t, {terminal = {}, populated = false, observerFailure = false, cancel = false, lowSpace = false, wrongDigest = false} = {}) {
  const directory = temporary(t);
  // Host root ownership is synthetic here: isolated workers may see the host's
  // unmapped root UID as nobody. No actual host systemd call is made by this suite.
  const launcher = '/usr/bin/true';
  const calls = [];
  const events = [];
  let stopped = false;
  let failedObserver = false;
  let spaceReads = 0;
  const signal = new AbortController();
  const execute = systemdExecutor({launcher, launcherSha256: wrongDigest ? '0'.repeat(64) : createHash('sha256').update(readFileSync(launcher)).digest('hex'), slice: 'synthetic-ci.slice'}, {
    lstat: path => ({...lstatSync(path), uid: 0, isFile: () => true}),
    delay: async () => {}, hasReserve: () => !lowSpace || ++spaceReads < 2,
    readFile: path => path.endsWith('/cpu.max') ? '50000 100000' : path.endsWith('/memory.max') ? '2621440000' : path.endsWith('/memory.swap.max') || path.endsWith('/cpuset.cpus.effective') ? '0' : `populated ${populated ? 1 : 0}\nfrozen 0`,
    command: async (file, args) => {
      calls.push({file, args});
      if (file.endsWith('/systemd-run')) return '';
      if (args[0] === 'stop') {stopped = true; return '';}
      assert.equal(args[0], 'show');
      if (observerFailure && !failedObserver) {failedObserver = true; throw Error('offline transient observation failure');}
      if (cancel && !stopped) signal.abort();
      const properties = stopped ? {LoadState: 'loaded', ActiveState: 'inactive', SubState: 'dead', MainPID: '0', Result: 'success', ExecMainCode: '1', ExecMainStatus: '0'} : {LoadState: 'loaded', ActiveState: 'active', SubState: 'exited', MainPID: '0', Result: 'success', ExecMainCode: '1', ExecMainStatus: '0', ...terminal};
      return Object.entries(properties).map(([key, value]) => `${key}=${value}`).join('\n');
    },
  });
  return {directory, launcher, calls, events, invoke: () => execute({sha, name, step: 0, argv: checkPlan(name)[0]}, {directory, runId: '12345678-1234-1234-1234-123456789abc', record: async event => events.push(event)}, signal.signal)};
}

test('supervisor requires actual exited status and confirmed full cleanup', async t => {
  const fixture = supervisorFixture(t);
  assert.deepEqual(await fixture.invoke(), passed());
  assert.equal(fixture.events.at(-1).stage, 'unit-stopped');
  const start = fixture.calls.find(call => call.file.endsWith('/systemd-run')).args;
  for (const flag of ['--property=RuntimeMaxSec=1800', '--property=KillMode=control-group', '--property=MemorySwapMax=0', '--property=CPUAffinity=0', '--slice=synthetic-ci.slice']) assert.ok(start.includes(flag));
  assert.deepEqual(start.slice(-3), [sha, name, '0']);
  assert.equal(fixture.calls.filter(call => call.args[0] === 'stop').length, 1);
});

for (const [label, terminal] of [
  ['nonzero', {ExecMainStatus: '7', Result: 'exit-code', SubState: 'failed', ActiveState: 'failed'}],
  ['signal', {ExecMainCode: '2', ExecMainStatus: '9', Result: 'signal', SubState: 'failed', ActiveState: 'failed'}],
  ['timeout', {Result: 'timeout', SubState: 'failed', ActiveState: 'failed'}],
]) {
  test(`${label} cannot return a green supervisor receipt`, async t => {
    const fixture = supervisorFixture(t, {terminal});
    const receipt = await fixture.invoke();
    assert.notEqual(receipt.exitCode, 0);
    assert.equal(receipt.timedOut, label === 'timeout');
    assert.equal(fixture.events.at(-1).stage, 'unit-stopped');
  });
}

test('observer failure retries the same unit without another start', async t => {
  const fixture = supervisorFixture(t, {observerFailure: true});
  assert.deepEqual(await fixture.invoke(), passed());
  assert.equal(fixture.calls.filter(call => call.file.endsWith('/systemd-run')).length, 1);
  assert.equal(fixture.calls.filter(call => call.args[0] === 'show').length, 3);
});

for (const option of ['cancel', 'lowSpace']) {
  test(`${option} stops the complete unit and returns cancellation`, async t => {
    const fixture = supervisorFixture(t, {[option]: true});
    assert.equal((await fixture.invoke()).cancelled, true);
    assert.equal(fixture.events.at(-1).stage, 'unit-stopped');
  });
}

test('remaining worker descendants fail cleanup, regardless of exit zero', async t => {
  const fixture = supervisorFixture(t, {populated: true});
  await assert.rejects(fixture.invoke(), /descendants remain/);
});

test('unexpected trusted launcher digest is rejected before creating any service', async t => {
  const fixture = supervisorFixture(t, {wrongDigest: true});
  await assert.rejects(fixture.invoke(), /launcher changed/);
  assert.deepEqual(fixture.calls, []);
});
