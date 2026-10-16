#!/usr/bin/node
// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Trusted host launcher; install outside candidates with a private host config.
import assert from 'node:assert/strict';
import {execFileSync, spawnSync} from 'node:child_process';
import {lstatSync, readFileSync, readlinkSync, realpathSync} from 'node:fs';
import {basename, dirname, isAbsolute, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {checkPlan} from './github-checks.mjs';

const binary = '/usr/bin/bwrap';
const boundary = ['--die-with-parent', '--new-session', '--ro-bind', '/usr', '/usr',
  '--symlink', 'usr/bin', '/bin', '--symlink', 'usr/lib', '/lib', '--symlink', 'usr/lib64', '/lib64',
  '--ro-bind', '/etc/passwd', '/etc/passwd', '--ro-bind', '/etc/group', '/etc/group',
  '--ro-bind', '/etc/nsswitch.conf', '/etc/nsswitch.conf', '--proc', '/proc', '--dev', '/dev', '--tmpfs', '/run'];
const verifySource = 'expected=$1; expected_uid=$2; shift 2; test "$(id -u)" = "$expected_uid"; test "$(git rev-parse HEAD)" = "$expected"; test -z "$(git status --porcelain)"; test ! -e /var/run/docker.sock; capabilities=$(while read -r key value remainder; do if [[ "$key" == CapEff: ]]; then printf "%s" "$value"; fi; done </proc/self/status); test "$capabilities" = 0000000000000000; ';

export function workerPlan(config, {sha, name, step}) {
  assert.match(sha, /^[0-9a-f]{40}$/);
  assert.ok(Number.isInteger(step) && step >= 0 && step < checkPlan(name).length, 'Invalid worker step');
  assert.equal(config.version, 1);
  const worker = name === 'focused-go' ? config.go : config.docker;
  assert.ok(worker && isAbsolute(worker.root) && /^public-gitea-[A-Za-z0-9._-]+$/.test(basename(worker.root)), 'Dedicated public task root is required');
  assert.match(worker.user, /^[a-z][a-z0-9-]{0,31}$/);
  assert.ok(Number.isSafeInteger(worker.uid) && worker.uid > 0);
  const at = path => join(worker.root, path);
  const mounts = name === 'focused-go'
    ? ['source', 'home', 'tmp', 'cache', 'scratch', 'deps/sdk', 'deps/mod']
    : ['source', 'home', 'buildkit'];
  let args;
  let environment;
  let inside;
  if (name === 'focused-go') {
    args = [binary, '--unshare-all', ...boundary,
      '--ro-bind', at('source'), '/workspace', '--bind', at('home'), '/home/worker',
      '--bind', at('tmp'), '/tmp', '--bind', at('cache'), '/cache', '--bind', at('scratch'), '/scratch',
      '--ro-bind', at('deps/sdk'), '/toolchain', '--ro-bind', at('deps/mod'), '/gomod'];
    environment = {PATH: '/toolchain/bin:/usr/bin:/bin', HOME: '/home/worker', LANG: 'C.UTF-8',
      GOMAXPROCS: '1', GOMEMLIMIT: '2GiB', GOTOOLCHAIN: 'local', GOWORK: 'off', GOPROXY: 'off',
      GOSUMDB: 'off', GOMODCACHE: '/gomod', GOCACHE: '/cache', TMPDIR: '/scratch'};
    inside = `${verifySource}test -z "$(/usr/sbin/ip route)"; exec "$@"`;
  } else {
    assert.match(worker.engineService, /^[a-zA-Z0-9-]+\.service$/);
    assert.match(worker.stateDirectory, /^[a-zA-Z0-9._-]+$/);
    args = [binary, '--unshare-user', '--unshare-pid', '--unshare-ipc', '--unshare-uts', ...boundary,
      '--ro-bind', '/etc/ssl/certs', '/etc/ssl/certs', '--tmpfs', '/tmp',
      '--ro-bind', at('source'), '/workspace', '--bind', at('home'), '/home/worker',
      '--ro-bind', at('buildkit'), '/work/buildkit'];
    environment = {PATH: '/usr/sbin:/usr/bin:/sbin:/bin', HOME: '/home/worker', LANG: 'C.UTF-8',
      DOCKER_HOST: 'unix:///work/buildkit/docker.sock', BUILDKIT_PROGRESS: 'plain',
      HTTP_PROXY: 'http://127.0.0.1:8888', HTTPS_PROXY: 'http://127.0.0.1:8888', NO_PROXY: 'localhost,127.0.0.1'};
    if (name === 'native-release') {
      assert.equal(config.release?.sourceCommit, sha, 'Native release source is not approved by the host');
      assert.match(config.release.version, /^v1\.27\.2-rinspace\.[1-9][0-9]*-rc\.[1-9][0-9]*$/);
      environment.SOURCE_COMMIT = sha;
      environment.GITEA_VERSION = config.release.version;
    }
    inside = `${verifySource}test -S /work/buildkit/docker.sock; docker info --format '{{range .SecurityOptions}}{{println .}}{{end}}' | /usr/bin/grep -Fx name=rootless; test "$(docker info --format '{{.DockerRootDir}}')" = /work/data; exec "$@"`;
  }
  args.push('--clearenv');
  for (const [key, value] of Object.entries(environment)) args.push('--setenv', key, value);
  args.push('--uid', String(worker.uid), '--gid', String(worker.uid), '--cap-drop', 'ALL', '--chdir', '/workspace',
    '/bin/bash', '-euo', 'pipefail', '-c', inside, 'isolated-check', sha, String(worker.uid), ...checkPlan(name)[step]);
  return {worker, mounts: mounts.map(at), file: '/usr/sbin/runuser', args: ['-u', worker.user, '--', ...args]};
}

function hostCommand(file, args) {
  return execFileSync(file, args, {encoding: 'utf8', timeout: 15000, maxBuffer: 1024 * 1024, env: {PATH: '/usr/bin:/bin'}}).trim();
}

export function launch(configPath, request) {
  assert.equal(process.getuid(), 0, 'Launcher must run on the trusted host');
  assert.equal(realpathSync(configPath), configPath);
  const configStat = lstatSync(configPath);
  assert.ok(configStat.isFile() && configStat.uid === 0 && (configStat.mode & 0o077) === 0, 'Host config must be private and root-owned');
  const plan = workerPlan(JSON.parse(readFileSync(configPath, 'utf8')), request);
  assert.equal(hostCommand('/usr/bin/id', ['-u', plan.worker.user]), String(plan.worker.uid));
  assert.equal(hostCommand('/usr/bin/id', ['-g', plan.worker.user]), String(plan.worker.uid));
  for (const path of [plan.worker.root, ...plan.mounts]) {
    assert.equal(realpathSync(path), path, 'Worker mount must not be a symlink');
    assert.ok(lstatSync(path).isDirectory(), 'Worker mount must be a directory');
    // Workers may write inside cache/home, never replace mount-point entries.
    for (let parent = path === plan.worker.root ? path : dirname(path); ; parent = dirname(parent)) {
      const stat = lstatSync(parent);
      assert.ok(stat.uid === 0 && (stat.mode & 0o022) === 0, 'Mount-point parents must be root-owned and not worker writable');
      if (parent === plan.worker.root) break;
      assert.ok(parent.startsWith(`${plan.worker.root}/`));
    }
  }
  let {file, args} = plan;
  if (['dockerfile-check', 'native-release'].includes(request.name)) {
    const service = plan.worker.engineService;
    const property = key => hostCommand('/usr/bin/systemctl', ['show', service, '-p', key, '--value']);
    assert.equal(property('ActiveState'), 'active', 'Dedicated rootless engine is unavailable');
    const outer = property('MainPID');
    assert.match(outer, /^[1-9][0-9]*$/);
    const hostNetwork = readlinkSync('/proc/1/ns/net');
    assert.notEqual(readlinkSync(`/proc/${outer}/ns/net`), hostNetwork, 'Engine shares host network');
    assert.equal(hostCommand('/usr/bin/nsenter', ['--target', outer, '--net', '--', '/usr/sbin/ip', 'route', 'show', 'default']), '', 'Engine outer namespace has a default route');
    const child = readFileSync(join(plan.worker.root, plan.worker.stateDirectory, 'child_pid'), 'utf8').trim();
    assert.match(child, /^[1-9][0-9]*$/);
    const controlGroup = property('ControlGroup');
    assert.ok(controlGroup.endsWith(`/${service}`));
    assert.equal(readFileSync(`/proc/${child}/cgroup`, 'utf8').trim(), `0::${controlGroup}`, 'Rootless child is not in its dedicated engine unit');
    assert.notEqual(readlinkSync(`/proc/${child}/ns/net`), hostNetwork);
    args = ['--target', child, '--net', '--', file, ...args];
    file = '/usr/bin/nsenter';
  }
  const result = spawnSync(file, args, {stdio: 'inherit', env: {PATH: '/usr/bin:/bin'}});
  if (result.error || result.signal || !Number.isInteger(result.status)) return 1;
  return result.status;
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    assert.equal(process.argv.length, 5, 'Expected SHA, check name and step number');
    assert.match(process.argv[4], /^(0|[1-9][0-9]*)$/);
    process.exitCode = launch(join(dirname(fileURLToPath(import.meta.url)), 'worker-config.json'), {sha: process.argv[2], name: process.argv[3], step: Number(process.argv[4])});
  } catch {
    console.error('Isolated worker refused to start or failed; inspect its fixed host configuration and recorded unit');
    process.exitCode = 1;
  }
}
