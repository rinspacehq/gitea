// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Install a reviewed copy on the trusted host, never import from a candidate.
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {createHash, randomUUID} from 'node:crypto';
import {closeSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, realpathSync, renameSync, rmdirSync, statfsSync, writeFileSync} from 'node:fs';
import {isAbsolute, join} from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {promisify} from 'node:util';
import {checkPlan} from './github-checks.mjs';

const exec = promisify(execFile);
async function command(file, args) {
  try {
    return (await exec(file, args, {encoding: 'utf8', timeout: 20000, maxBuffer: 1024 * 1024, env: {PATH: '/usr/bin:/bin'}})).stdout.trim();
  } catch {
    throw new Error('Native supervisor command failed; inspect the recorded unit before retrying');
  }
}

function trustedDirectory(directory) {
  assert.ok(isAbsolute(directory) && realpathSync(directory) === directory, 'Ledger directory must be canonical');
  const stat = lstatSync(directory);
  assert.ok(stat.isDirectory() && stat.uid === process.getuid() && (stat.mode & 0o077) === 0, 'Ledger must be private and owned by the controller');
}

function atomicJson(path, value) {
  const temporary = `${path}.${randomUUID()}.next`;
  const fd = openSync(temporary, 'wx', 0o600);
  try {writeFileSync(fd, `${JSON.stringify(value, null, 2)}\n`); fsyncSync(fd);} finally {closeSync(fd);}
  renameSync(temporary, path);
}

function syncDirectory(directory) {
  const fd = openSync(directory, 'r');
  try {fsyncSync(fd);} finally {closeSync(fd);}
}

export async function runNativePullCheck(options) {
  assert.ok(Number.isSafeInteger(options.pullNumber) && options.pullNumber > 0);
  return runNativeCheck(options, 'runPullCheck');
}

export async function runNativeMaintenanceCheck(options) {
  assert.equal(options.pullNumber, undefined, 'Maintenance checks do not impersonate a PR');
  return runNativeCheck(options, 'runMaintenanceCheck');
}

async function runNativeCheck({directory, reporter, pullNumber, sha, name, execute}, method) {
  trustedDirectory(directory);
  assert.match(sha, /^[0-9a-f]{40}$/);
  checkPlan(name);
  assert.equal(typeof execute, 'function');
  const lock = join(directory, 'active');
  // Never infer that an old PID/observer means an abandoned run is safe to retry.
  mkdirSync(lock, {mode: 0o700});
  const path = join(directory, `${sha}-${name}.json`);
  const state = {version: 1, runId: randomUUID(), controllerPid: process.pid, pullNumber, sha, name, status: 'reserved', events: []};
  const reservation = openSync(path, 'wx', 0o600);
  closeSync(reservation);
  const record = async event => {
    state.events.push({...event, at: new Date().toISOString()});
    atomicJson(path, state);
    syncDirectory(directory);
  };
  await record({stage: 'reserved'});
  // Any exception retains the lock and durable identity for explicit reconciliation.
  // Only an unambiguous terminal result permits the next run to acquire the lock.
  const abort = new AbortController();
  const cancel = () => abort.abort();
  process.on('SIGINT', cancel);
  process.on('SIGTERM', cancel);
  let result;
  let uncertain = false;
  try {
    result = await reporter[method]({...(pullNumber === undefined ? {} : {pullNumber}), sha, name, record, execute: async step => {
      try {
        await record({stage: 'step-starting', step: step.step});
        const receipt = await execute(step, {directory, runId: state.runId, record}, abort.signal);
        await record({stage: 'step-finished', step: step.step, receipt});
        return receipt;
      } catch {
        uncertain = true;
        throw new Error('Native execution requires reconciliation');
      }
    }});
    assert.ok(!uncertain, 'Inspect the recorded worker; cleanup or execution was uncertain');
  } finally {
    process.removeListener('SIGINT', cancel);
    process.removeListener('SIGTERM', cancel);
  }
  state.status = 'completed';
  state.result = result;
  await record({stage: 'completed'});
  rmdirSync(lock);
  syncDirectory(directory);
  return result;
}

// The pinned launcher supplies all namespaces/mounts and must fail closed if any
// isolation prerequisite is missing. The candidate cannot select its executable.
export function systemdExecutor({launcher, launcherSha256, slice}, adapters = {}) {
  assert.ok(isAbsolute(launcher) && realpathSync(launcher) === launcher);
  const stat = (adapters.lstat ?? lstatSync)(launcher);
  assert.ok(stat.isFile() && stat.uid === 0 && (stat.mode & 0o022) === 0 && (stat.mode & 0o111) !== 0, 'Launcher must be root-owned and not group/world writable');
  assert.match(launcherSha256, /^[0-9a-f]{64}$/);
  assert.match(slice, /^[a-zA-Z0-9-]+\.slice$/);
  const run = adapters.command ?? command;
  const wait = adapters.delay ?? (() => delay(1000));
  const read = adapters.readFile ?? (path => readFileSync(path, 'utf8').trim());
  const reserve = adapters.hasReserve ?? (directory => ['/', directory].every(path => {
    const space = statfsSync(path);
    return space.bavail * space.bsize >= 1500000000;
  }));
  return async (step, {directory, runId, record}, signal) => {
    assert.match(runId, /^[0-9a-f-]{36}$/);
    assert.deepEqual(step.argv, checkPlan(step.name)[step.step], 'Unknown worker command');
    assert.match(step.sha, /^[0-9a-f]{40}$/);
    assert.equal(createHash('sha256').update(readFileSync(launcher)).digest('hex'), launcherSha256, 'Trusted launcher changed');
    assert.equal(read(`/sys/fs/cgroup/${slice}/cpu.max`), '50000 100000', 'Aggregate half-CPU limit is required');
    assert.equal(read(`/sys/fs/cgroup/${slice}/memory.max`), '2621440000');
    assert.equal(read(`/sys/fs/cgroup/${slice}/memory.swap.max`), '0');
    assert.equal(read(`/sys/fs/cgroup/${slice}/cpuset.cpus.effective`), '0');
    trustedDirectory(directory);
    const unit = `rinspace-native-${runId}-${step.step}.service`;
    const logPath = join(directory, `${runId}-${step.step}.log`);
    const fd = openSync(logPath, 'wx', 0o600);
    closeSync(fd);
    const deadline = step.name === 'native-release' ? 10800 : step.name === 'focused-go' ? 1800 : 600;
    const started = Date.now();
    let cancelled = signal?.aborted === true;
    let timedOut = false;
    let terminal;
    const show = async () => {
      for (let attempt = 0; ; attempt++) {
        try {
          return Object.fromEntries((await run('/usr/bin/systemctl', ['show', unit, '-p', 'LoadState', '-p', 'ActiveState', '-p', 'SubState', '-p', 'MainPID', '-p', 'Result', '-p', 'ExecMainCode', '-p', 'ExecMainStatus'])).split('\n').map(line => line.split('=')));
        } catch (error) {
          if (attempt >= 2) throw error;
          await wait(); // Observe the same unit again; never repeat its launch.
        }
      }
    };
    if (cancelled) return {exitCode: null, signal: null, timedOut: false, cancelled: true};
    assert.ok(reserve(directory), 'CI scratch reserve breached');
    await record({stage: 'unit-starting', unit, logPath, deadline});
    try {
      await run('/usr/bin/systemd-run', ['--quiet', `--unit=${unit}`, `--slice=${slice}`,
        '--property=Type=exec', '--property=RemainAfterExit=yes', '--property=KillMode=control-group',
        `--property=RuntimeMaxSec=${deadline}`, '--property=TimeoutStopSec=10', '--property=MemorySwapMax=0',
        `--property=MemoryMax=${step.name === 'focused-go' ? '2100M' : '256M'}`,
        '--property=TasksMax=256', '--property=CPUAffinity=0', '--property=Nice=19', '--property=IOSchedulingClass=idle',
        `--property=StandardOutput=append:${logPath}`, `--property=StandardError=append:${logPath}`,
        launcher, step.sha, step.name, String(step.step)]);
      for (;;) {
        terminal = await show();
        assert.equal(terminal.LoadState, 'loaded', 'Lost worker unit; do not restart');
        cancelled = signal?.aborted === true || !reserve(directory);
        timedOut = terminal.Result === 'timeout' || Date.now() - started >= deadline * 1000;
        if (cancelled || timedOut || ['exited', 'failed', 'dead'].includes(terminal.SubState)) break;
        assert.ok(['activating', 'active', 'deactivating'].includes(terminal.ActiveState), 'Unknown worker state');
        await wait();
      }
      await record({stage: 'unit-observed', unit, terminal, cancelled, timedOut});
    } finally {
      // This also reconciles an ambiguous systemd-run response; never repeat start.
      await run('/usr/bin/systemctl', ['stop', unit]);
      const stopped = await show();
      assert.ok(stopped.MainPID === '0' && ['inactive', 'failed'].includes(stopped.ActiveState), 'Worker cleanup not confirmed');
      let events;
      try {events = read(`/sys/fs/cgroup/${slice}/${unit}/cgroup.events`);} catch (error) {
        if (error.code !== 'ENOENT') throw error;
        events = 'removed';
      }
      assert.ok(events === 'removed' || /^populated 0$/m.test(events), 'Worker descendants remain');
      await record({stage: 'unit-stopped', unit, stopped, cgroupEvents: events});
    }
    const passed = !cancelled && !timedOut && terminal.Result === 'success' && terminal.ExecMainCode === '1' && terminal.ExecMainStatus === '0' && terminal.MainPID === '0' && terminal.SubState === 'exited';
    return {exitCode: passed ? 0 : 1, signal: terminal.ExecMainCode === '2' || terminal.ExecMainCode === '3' ? 'worker-signal' : null, timedOut, cancelled};
  };
}
