// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Trusted host-side transport. Never import this module from a candidate checkout.
import assert from 'node:assert/strict';
import {createPrivateKey, randomUUID, sign} from 'node:crypto';

const repository = 'rinspacehq/gitea';
const basePath = `/repos/${repository}`;
const permissions = Object.freeze({checks: 'write', contents: 'read', pull_requests: 'read', metadata: 'read'});
const goSteps = ['formatting', 'models-control', 'authentication', 'repository', 'editor-catalog', 'migration'];
const plans = Object.freeze({
  'focused-go': goSteps.map(step => ['bash', 'contrib/rinspace/ci-check.sh', step]),
  'dockerfile-check': [
    ['docker', 'buildx', 'build', '--check', '--platform', 'linux/amd64', '.'],
    ['docker', 'buildx', 'build', '--check', '--platform', 'linux/amd64', '--file', 'Dockerfile.rootless', '.'],
  ],
  'native-release': [['bash', 'contrib/rinspace/native-build.sh']],
});
for (const plan of Object.values(plans)) {
  plan.forEach(Object.freeze);
  Object.freeze(plan);
}

function positiveId(value) {
  assert.ok(Number.isSafeInteger(value) && value > 0, 'A positive numeric GitHub ID is required');
}

function minimalPermissions(actual) {
  assert.deepEqual(actual, permissions, 'Unexpected GitHub App permissions');
}

export function checkPlan(name) {
  assert.ok(Object.hasOwn(plans, name), 'Unknown required check');
  return plans[name];
}

export function appJwt(appId, privateKey, now = Date.now()) {
  positiveId(appId);
  const key = createPrivateKey(privateKey);
  assert.equal(key.asymmetricKeyType, 'rsa', 'GitHub App JWT requires an RSA private key');
  const seconds = Math.floor(now / 1000);
  const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url');
  const data = `${encode({alg: 'RS256', typ: 'JWT'})}.${encode({iss: String(appId), iat: seconds - 60, exp: seconds + 540})}`;
  return `${data}.${sign('RSA-SHA256', Buffer.from(data), key).toString('base64url')}`;
}

export class GithubChecks {
  #config;
  #fetch;
  #now;
  #token;
  #expires = 0;

  constructor({appId, installationId, appSlug, privateKey}, {fetch = globalThis.fetch, now = Date.now} = {}) {
    positiveId(appId);
    positiveId(installationId);
    assert.match(appSlug, /^[a-z0-9][a-z0-9-]{0,99}$/);
    // Validate locally before any API request; credentials remain host-side.
    appJwt(appId, privateKey, now());
    this.#config = {appId, installationId, appSlug, privateKey};
    this.#fetch = fetch;
    this.#now = now;
  }

  async #request(method, path, token, body) {
    let response;
    try {
      response = await this.#fetch(`https://api.github.com${path}`, {
        method, redirect: 'error', signal: AbortSignal.timeout(15000),
        headers: {'Accept': 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'rinspace-independent-ci', 'Authorization': `Bearer ${token}`, ...(body ? {'Content-Type': 'application/json'} : {})},
        ...(body ? {body: JSON.stringify(body)} : {}),
      });
    } catch {
      // Do not surface request objects, tokens or arbitrary response bodies.
      throw new Error('GitHub request failed; inspect existing check before retrying');
    }
    assert.ok(response.ok, `GitHub rejected ${method} ${path} (${response.status})`);
    try {
      return await response.json();
    } catch {
      throw new Error('GitHub returned invalid JSON');
    }
  }

  async #authorize() {
    if (this.#token && this.#expires > this.#now() + 60000) return this.#token;
    const {appId, installationId, appSlug, privateKey} = this.#config;
    const jwt = appJwt(appId, privateKey, this.#now());
    const app = await this.#request('GET', '/app', jwt);
    assert.ok(app.id === appId && app.slug === appSlug && app.owner?.login === 'rinspacehq', 'GitHub App identity mismatch');
    minimalPermissions(app.permissions);
    const installation = await this.#request('GET', `${basePath}/installation`, jwt);
    assert.ok(installation.id === installationId && installation.app_id === appId && installation.account?.login === 'rinspacehq' && installation.repository_selection === 'selected' && !installation.suspended_at, 'Unexpected GitHub App installation');
    minimalPermissions(installation.permissions);
    const issued = await this.#request('POST', `/app/installations/${installationId}/access_tokens`, jwt, {repositories: ['gitea'], permissions});
    assert.ok(typeof issued.token === 'string' && issued.token.length > 0, 'Missing installation token');
    minimalPermissions(issued.permissions);
    const expires = Date.parse(issued.expires_at);
    assert.ok(Number.isFinite(expires) && expires > this.#now() + 60000, 'Installation token is expired');
    const available = await this.#request('GET', '/installation/repositories?per_page=100', issued.token);
    assert.ok(available.total_count === 1 && available.repositories?.length === 1 && available.repositories[0].full_name === repository, 'Installation token must address only canonical Gitea');
    this.#token = issued.token;
    this.#expires = expires;
    return issued.token;
  }

  async #currentPull(pullNumber, sha, token) {
    const pull = await this.#request('GET', `${basePath}/pulls/${pullNumber}`, token);
    return pull.state === 'open' && pull.head?.sha === sha && pull.head.repo?.full_name === repository && pull.base?.repo?.full_name === repository && pull.base.ref === 'rinspace-1.27';
  }

  async runPullCheck(options) {
    positiveId(options.pullNumber);
    return this.#runCheck({...options, currentSource: token => this.#currentPull(options.pullNumber, options.sha, token)});
  }

  async runMaintenanceCheck(options) {
    return this.#runCheck({...options, currentSource: async token => {
      const branch = await this.#request('GET', `${basePath}/branches/rinspace-1.27`, token);
      return branch.name === 'rinspace-1.27' && branch.protected === true && branch.commit?.sha === options.sha;
    }});
  }

  async #runCheck({sha, name, execute, record = async () => {}, currentSource}) {
    assert.ok(['focused-go', 'dockerfile-check'].includes(name), 'Unknown PR check; native releases require the separate publisher');
    assert.match(sha, /^[0-9a-f]{40}$/);
    assert.equal(typeof execute, 'function', 'An isolated executor is required');
    assert.equal(typeof record, 'function', 'A trusted ledger callback is required');
    const plan = checkPlan(name);
    let token = await this.#authorize();
    assert.ok(await currentSource(token), 'Source is not the reviewed canonical head');
    const externalId = `rinspace-native-ci/${randomUUID()}`;
    await record({stage: 'check-creating', externalId, sha, name});
    const created = await this.#request('POST', `${basePath}/check-runs`, token, {name, head_sha: sha, status: 'in_progress', external_id: externalId, started_at: new Date(this.#now()).toISOString()});
    positiveId(created.id);
    assert.ok(created.app?.id === this.#config.appId && created.head_sha === sha && created.name === name && created.status === 'in_progress', 'Created check identity mismatch');
    await record({stage: 'check-created', externalId, checkId: created.id});

    let conclusion = 'success';
    let completed = 0;
    for (const [step, argv] of plan.entries()) {
      let result;
      try {
        // No auth objects, environment or key paths cross the executor boundary.
        result = await execute(Object.freeze({sha, name, step, argv}));
      } catch {
        conclusion = 'failure';
        break;
      }
      if (result?.cancelled === true) conclusion = 'cancelled';
      else if (result?.timedOut === true) conclusion = 'timed_out';
      else if (!(result?.exitCode === 0 && result.signal === null && result.timedOut === false && result.cancelled === false)) conclusion = 'failure';
      if (conclusion !== 'success') break;
      completed++;
    }
    token = await this.#authorize();
    if (!await currentSource(token)) conclusion = 'cancelled';
    const current = await this.#request('GET', `${basePath}/check-runs/${created.id}`, token);
    assert.ok(current.id === created.id && current.app?.id === this.#config.appId && current.head_sha === sha && current.name === name && current.status === 'in_progress', 'Refusing to overwrite an unrelated or terminal check');
    await record({stage: 'check-completing', checkId: created.id, conclusion});
    const finished = await this.#request('PATCH', `${basePath}/check-runs/${created.id}`, token, {status: 'completed', conclusion, completed_at: new Date(this.#now()).toISOString(), output: {title: `${name}: ${conclusion}`, summary: `${completed}/${plan.length} isolated steps completed for ${sha}. Source was revalidated before completion.`}});
    assert.ok(finished.id === created.id && finished.app?.id === this.#config.appId && finished.head_sha === sha && finished.name === name && finished.status === 'completed' && finished.conclusion === conclusion, 'Completed check identity mismatch');
    await record({stage: 'check-completed', checkId: created.id, conclusion});
    return Object.freeze({id: created.id, sha, name, conclusion, completed, total: plan.length});
  }
}
