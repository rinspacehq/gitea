#!/usr/bin/env bash
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
# Run only through the reviewed namespace launcher and dedicated rootless engine.
set -euo pipefail
umask 077
test "$#" = 0
[[ "${SOURCE_COMMIT:-}" =~ ^[0-9a-f]{40}$ ]]
[[ "${GITEA_VERSION:-}" =~ ^v1\.27\.2-rinspace\.[1-9][0-9]*-rc\.[1-9][0-9]*$ ]]
test "${HOME:-}" = /home/worker
test "${DOCKER_HOST:-}" = unix:///work/buildkit/docker.sock
test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"
test -z "$(git status --porcelain)"
docker info --format '{{range .SecurityOptions}}{{println .}}{{end}}' | /usr/bin/grep -Fx name=rootless
test "$(docker info --format '{{.DockerRootDir}}')" = /work/data
docker buildx inspect rinspace-native-release | /usr/bin/grep -Ex 'Driver:[[:space:]]+docker-container'
output="$HOME/native-release/$GITEA_VERSION"
test ! -e "$output"
mkdir -p -m 700 "$output"
# "host" here is the validated isolated engine-child network, never host net.
# Publishing and signing are separate trusted-host operations, not build args.
docker buildx build --builder rinspace-native-release --platform linux/amd64 --file Dockerfile --network host \
  --build-arg "GITEA_VERSION=$GITEA_VERSION" \
  --build-arg HTTP_PROXY=http://127.0.0.1:8888 --build-arg HTTPS_PROXY=http://127.0.0.1:8888 \
  --label org.opencontainers.image.source=https://github.com/rinspacehq/gitea \
  --label "org.opencontainers.image.revision=$SOURCE_COMMIT" \
  --label "org.opencontainers.image.version=$GITEA_VERSION" \
  --label org.opencontainers.image.licenses=MIT \
  --label org.opencontainers.image.base.name=docker.io/library/alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 \
  --provenance mode=max --sbom true --metadata-file "$output/build-metadata.json" \
  --output "type=oci,dest=$output/image.oci.tar" .
test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"
test -z "$(git status --porcelain)"
