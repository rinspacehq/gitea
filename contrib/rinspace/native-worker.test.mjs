// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Offline launch-plan contracts, not a real namespace or Docker proof.
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {checkPlan} from './github-checks.mjs';
import {workerPlan} from './native-worker.mjs';

const sha = 'a'.repeat(40);
const config = {version: 1,
  go: {root: '/synthetic/public-gitea-go', user: 'synthetic-go', uid: 1201},
  docker: {root: '/synthetic/public-gitea-docker', user: 'synthetic-docker', uid: 1202, engineService: 'synthetic-engine.service', stateDirectory: 'rk-state'},
};
const request = (name = 'focused-go', step = 0) => ({sha, name, step});

test('every Go check has one fixed readonly source and offline dependency plan', () => {
  for (let step = 0; step < 6; step++) {
    const plan = workerPlan(config, request('focused-go', step));
    assert.equal(plan.file, '/usr/sbin/runuser');
    assert.deepEqual(plan.args.slice(0, 4), ['-u', 'synthetic-go', '--', '/usr/bin/bwrap']);
    assert.ok(plan.args.includes('--unshare-all') && plan.args.includes('--clearenv'));
    assert.deepEqual(plan.args.slice(plan.args.indexOf('/synthetic/public-gitea-go/source') - 1, plan.args.indexOf('/synthetic/public-gitea-go/source') + 2), ['--ro-bind', '/synthetic/public-gitea-go/source', '/workspace']);
    assert.ok(!plan.args.includes('--share-net') && !plan.args.includes('--cap-add'));
    assert.ok(plan.args.includes('GOPROXY') && plan.args.includes('off'));
    assert.deepEqual(plan.args.slice(-checkPlan('focused-go')[step].length), checkPlan('focused-go')[step]);
    assert.deepEqual(plan.mounts, ['source', 'home', 'tmp', 'cache', 'scratch', 'deps/sdk', 'deps/mod'].map(path => `${config.go.root}/${path}`));
  }
});

test('both Docker plans use only the dedicated rootless socket and exact recipe', () => {
  for (let step = 0; step < 2; step++) {
    const plan = workerPlan(config, request('dockerfile-check', step));
    assert.ok(plan.args.includes('unix:///work/buildkit/docker.sock'));
    assert.ok(plan.args.includes('--unshare-user') && plan.args.includes('--unshare-pid'));
    assert.ok(!plan.args.includes('--unshare-all'));
    assert.deepEqual(plan.mounts, ['source', 'home', 'buildkit'].map(path => `${config.docker.root}/${path}`));
    assert.deepEqual(plan.args.slice(-checkPlan('dockerfile-check')[step].length), checkPlan('dockerfile-check')[step]);
    assert.ok(!plan.args.some(value => value.includes('/var/run/docker.sock') && value.startsWith('/')));
    const inside = plan.args[plan.args.indexOf('-c') + 1];
    assert.match(inside, /name=rootless/);
    assert.match(inside, /DockerRootDir/);
  }
});

test('neither launch plan forwards caller credentials, arbitrary environment or paths', () => {
  const original = process.env.GITHUB_TOKEN;
  process.env.GITHUB_TOKEN = 'synthetic-must-not-forward';
  try {
    for (const name of ['focused-go', 'dockerfile-check']) {
      const plan = workerPlan(config, request(name));
      const encoded = JSON.stringify(plan);
      assert.ok(!encoded.includes('synthetic-must-not-forward') && !encoded.includes('GITHUB_TOKEN'));
      assert.ok(!plan.args.includes('/root') && !plan.args.includes('/home'));
      const inside = plan.args[plan.args.indexOf('-c') + 1];
      assert.match(inside, /git rev-parse HEAD/);
      assert.match(inside, /git status --porcelain/);
      assert.match(inside, /CapEff/);
      assert.deepEqual(plan.args.slice(plan.args.indexOf('--cap-drop'), plan.args.indexOf('--cap-drop') + 2), ['--cap-drop', 'ALL']);
    }
  } finally {
    if (original === undefined) delete process.env.GITHUB_TOKEN;
    else process.env.GITHUB_TOKEN = original;
  }
});

for (const invalid of [
  {sha: 'main'}, {sha: `${sha};true`}, {step: -1}, {step: 6}, {step: '0'}, {name: 'constructor'}, {name: '../focused-go'},
]) {
  test(`rejects invalid request ${JSON.stringify(invalid)}`, () => assert.throws(() => workerPlan(config, {...request(), ...invalid})));
}

test('unscoped roots, root users and arbitrary engine arguments are rejected', () => {
  for (const go of [{...config.go, root: '/'}, {...config.go, root: 'public-gitea-relative'}, {...config.go, uid: 0}, {...config.go, user: 'worker --root'}]) {
    assert.throws(() => workerPlan({...config, go}, request()));
  }
  for (const docker of [{...config.docker, engineService: 'host.service --all'}, {...config.docker, stateDirectory: '../../other'}]) {
    assert.throws(() => workerPlan({...config, docker}, request('dockerfile-check')));
  }
});
