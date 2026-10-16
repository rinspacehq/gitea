// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT
// Trusted publisher contract. Never expose a signing key to a build worker.
import assert from 'node:assert/strict';
import {createHash, createPrivateKey, createPublicKey, sign, verify} from 'node:crypto';

const type = 'application/vnd.rinspace.native-gitea-build.v1+json';
const sha = /^[0-9a-f]{40}$/;
const digest = /^sha256:[0-9a-f]{64}$/;
const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
const hash = value => createHash('sha256').update(value).digest('hex');
const positive = value => assert.ok(Number.isSafeInteger(value) && value > 0, 'Invalid native numeric identity');
function keys(value, expected) {
  assert.ok(value && typeof value === 'object' && !Array.isArray(value));
  assert.deepEqual(Object.keys(value).sort(), [...expected].sort(), 'Native contract fields differ');
}
function encoded(value) {
  assert.equal(typeof value, 'string');
  const bytes = Buffer.from(value, 'base64');
  assert.equal(bytes.toString('base64'), value, 'Noncanonical base64');
  return bytes;
}
function pae(payload) {
  return Buffer.concat([Buffer.from(`DSSEv1 ${Buffer.byteLength(type)} ${type} ${payload.length} `), payload]);
}
function publicIdentity(key) {
  assert.equal(key.asymmetricKeyType, 'rsa');
  assert.ok(key.asymmetricKeyDetails.modulusLength >= 2048, 'RSA key is too small');
  const pem = key.export({format: 'pem', type: 'spki'}).toString();
  return {pem, sha256: hash(pem)};
}

export function validateNativeBuild(payload) {
  keys(payload, ['schemaVersion', 'version', 'repository', 'source', 'image', 'build', 'statements', 'checks']);
  assert.equal(payload.schemaVersion, 'rinspace-native-gitea-build/v1');
  assert.match(payload.version, /^v1\.27\.2-rinspace\.[1-9][0-9]*-rc\.[1-9][0-9]*$/);
  assert.equal(payload.repository, 'rinspacehq/gitea');
  keys(payload.source, ['branch', 'commit', 'tree']);
  assert.equal(payload.source.branch, 'rinspace-1.27');
  assert.match(payload.source.commit, sha); assert.match(payload.source.tree, sha);
  keys(payload.image, ['repository', 'indexDigest', 'platform', 'platformDigest', 'configDigest']);
  assert.equal(payload.image.repository, 'ghcr.io/rinspacehq/gitea/runtime');
  assert.equal(payload.image.platform, 'linux/amd64');
  for (const name of ['indexDigest', 'platformDigest', 'configDigest']) assert.match(payload.image[name], digest);
  assert.notEqual(payload.image.indexDigest, payload.image.platformDigest);
  keys(payload.build, ['appId', 'appSlug', 'checkRunId', 'externalId', 'controllerCommit', 'recipeSha256', 'logDigest', 'startedAt', 'completedAt']);
  positive(payload.build.appId); positive(payload.build.checkRunId);
  assert.match(payload.build.appSlug, /^[a-z0-9][a-z0-9-]{0,99}$/);
  assert.match(payload.build.externalId, new RegExp(`^rinspace-native-release/${uuid}$`));
  assert.match(payload.build.controllerCommit, sha);
  assert.match(payload.build.recipeSha256, digest); assert.match(payload.build.logDigest, digest);
  for (const field of ['startedAt', 'completedAt']) {
    assert.ok(Number.isFinite(Date.parse(payload.build[field])));
    assert.equal(new Date(payload.build[field]).toISOString(), payload.build[field]);
  }
  assert.ok(Date.parse(payload.build.completedAt) >= Date.parse(payload.build.startedAt));
  assert.ok(Date.parse(payload.build.completedAt) - Date.parse(payload.build.startedAt) <= 86400000);
  keys(payload.statements, ['sbomDigest', 'provenanceDigest']);
  for (const value of Object.values(payload.statements)) assert.match(value, digest);
  assert.ok(Array.isArray(payload.checks));
  assert.deepEqual(payload.checks.map(check => check.name).sort(), ['dockerfile-check', 'focused-go']);
  const ids = new Set([payload.build.checkRunId]);
  for (const check of payload.checks) {
    keys(check, ['name', 'id', 'sha', 'appId', 'conclusion']);
    positive(check.id); assert.ok(!ids.has(check.id)); ids.add(check.id);
    assert.equal(check.sha, payload.source.commit);
    assert.equal(check.appId, payload.build.appId);
    assert.equal(check.conclusion, 'success');
  }
  return payload;
}

export function signNativeReceipt(payload, privateKey) {
  validateNativeBuild(payload);
  const key = createPrivateKey(privateKey);
  assert.equal(key.asymmetricKeyType, 'rsa');
  const identity = publicIdentity(createPublicKey(key));
  const bytes = Buffer.from(`${JSON.stringify(payload)}\n`);
  return {
    schemaVersion: 'rinspace-native-gitea-receipt/v1',
    publicKeyPem: identity.pem,
    envelope: {payloadType: type, payload: bytes.toString('base64'), signatures: [{keyid: identity.sha256, sig: sign('RSA-SHA256', pae(bytes), key).toString('base64')}]},
  };
}

export function verifyNativeReceipt(receipt, trust) {
  keys(trust, ['appId', 'appSlug', 'publicKeySha256']);
  positive(trust.appId); assert.match(trust.publicKeySha256, /^[0-9a-f]{64}$/);
  keys(receipt, ['schemaVersion', 'publicKeyPem', 'envelope']);
  assert.equal(receipt.schemaVersion, 'rinspace-native-gitea-receipt/v1');
  const key = createPublicKey(receipt.publicKeyPem);
  const identity = publicIdentity(key);
  assert.equal(identity.pem, receipt.publicKeyPem);
  assert.equal(identity.sha256, trust.publicKeySha256, 'Native publisher key is not trusted');
  keys(receipt.envelope, ['payloadType', 'payload', 'signatures']);
  assert.equal(receipt.envelope.payloadType, type);
  const bytes = encoded(receipt.envelope.payload);
  assert.ok(Array.isArray(receipt.envelope.signatures) && receipt.envelope.signatures.length === 1);
  const signature = receipt.envelope.signatures[0];
  keys(signature, ['keyid', 'sig']);
  assert.equal(signature.keyid, identity.sha256);
  assert.ok(verify('RSA-SHA256', pae(bytes), key, encoded(signature.sig)), 'Native signature is invalid');
  const payload = validateNativeBuild(JSON.parse(bytes.toString('utf8')));
  assert.equal(bytes.toString('utf8'), `${JSON.stringify(payload)}\n`, 'Noncanonical native payload');
  assert.equal(payload.build.appId, trust.appId);
  assert.equal(payload.build.appSlug, trust.appSlug);
  return payload;
}
