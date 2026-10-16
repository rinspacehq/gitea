// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import {createHash, generateKeyPairSync} from 'node:crypto';
import {test} from 'node:test';
import {signNativeReceipt, verifyNativeReceipt} from './native-receipt.mjs';

const key = generateKeyPairSync('rsa', {modulusLength: 2048});
const privateKey = key.privateKey.export({type: 'pkcs8', format: 'pem'});
const publicKey = key.publicKey.export({type: 'spki', format: 'pem'});
const trust = {appId: 123, appSlug: 'example-native-ci', publicKeySha256: createHash('sha256').update(publicKey).digest('hex')};
const digest = value => `sha256:${value.repeat(64)}`;
function fixture() {
  return {schemaVersion: 'rinspace-native-gitea-build/v1', version: 'v1.27.2-rinspace.2-rc.1', repository: 'rinspacehq/gitea',
    source: {branch: 'rinspace-1.27', commit: 'a'.repeat(40), tree: 'b'.repeat(40)},
    image: {repository: 'ghcr.io/rinspacehq/gitea/runtime', indexDigest: digest('a'), platform: 'linux/amd64', platformDigest: digest('b'), configDigest: digest('c')},
    build: {appId: 123, appSlug: 'example-native-ci', checkRunId: 101, externalId: 'rinspace-native-release/12345678-1234-1234-1234-123456789abc', controllerCommit: 'c'.repeat(40), recipeSha256: digest('d'), logDigest: digest('e'), startedAt: '2026-01-01T00:00:00.000Z', completedAt: '2026-01-01T00:01:00.000Z'},
    statements: {sbomDigest: digest('f'), provenanceDigest: digest('0')},
    checks: [{name: 'focused-go', id: 102, sha: 'a'.repeat(40), appId: 123, conclusion: 'success'}, {name: 'dockerfile-check', id: 103, sha: 'a'.repeat(40), appId: 123, conclusion: 'success'}]};
}
test('synthetic signed native receipt verifies without invented Actions identity', () => {
  const payload = fixture();
  assert.deepEqual(verifyNativeReceipt(signNativeReceipt(payload, privateKey), trust), payload);
});
for (const [name, mutate] of Object.entries({
  repository: p => {p.repository = 'other/gitea';},
  branch: p => {p.source.branch = 'main';},
  version: p => {p.version = 'latest';},
  digest: p => {p.image.configDigest = 'latest';},
  'index/platform collision': p => {p.image.platformDigest = p.image.indexDigest;},
  failure: p => {p.checks[0].conclusion = 'failure';},
  'missing check': p => {p.checks.pop();},
  'duplicate check': p => {p.checks[1].name = p.checks[0].name;},
  'check source': p => {p.checks[0].sha = 'd'.repeat(40);},
  'check App': p => {p.checks[0].appId++;},
  'check identity reuse': p => {p.checks[0].id = p.build.checkRunId;},
  'old fake workflow field': p => {p.build.workflowRunId = 101;},
  'backwards time': p => {p.build.completedAt = '2025-01-01T00:00:00.000Z';},
  'noncanonical time': p => {p.build.startedAt = '2026-01-01';},
})) test(`refuses incomplete or rebound ${name}`, () => {
  const payload = fixture(); mutate(payload);
  assert.throws(() => signNativeReceipt(payload, privateKey));
});
test('changing payload bytes invalidates its signature', () => {
  const receipt = signNativeReceipt(fixture(), privateKey);
  receipt.envelope.payload = Buffer.from(JSON.stringify({...fixture(), version: 'v1.27.2-rinspace.3-rc.1'})+'\n').toString('base64');
  assert.throws(() => verifyNativeReceipt(receipt, trust), /signature/);
});
test('rejects another validly signed publisher even with rebound receipt hashes', () => {
  const other = generateKeyPairSync('rsa', {modulusLength: 2048}).privateKey.export({type: 'pkcs8', format: 'pem'});
  assert.throws(() => verifyNativeReceipt(signNativeReceipt(fixture(), other), trust), /not trusted/);
});
for (const altered of [{...trust, appId: 124}, {...trust, appSlug: 'other-ci'}]) test('pins the actual App independently from the signature', () => {
  assert.throws(() => verifyNativeReceipt(signNativeReceipt(fixture(), privateKey), altered));
});
test('does not accept another payload type or multiple signatures', () => {
  const receipt = signNativeReceipt(fixture(), privateKey);
  receipt.envelope.payloadType = 'application/jwt';
  assert.throws(() => verifyNativeReceipt(receipt, trust));
  receipt.envelope.payloadType = 'application/vnd.rinspace.native-gitea-build.v1+json';
  receipt.envelope.signatures.push(receipt.envelope.signatures[0]);
  assert.throws(() => verifyNativeReceipt(receipt, trust));
});
