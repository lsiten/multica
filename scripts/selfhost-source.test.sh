#!/usr/bin/env bash
# Guard deployment from CI images, with local compilation explicitly opt-in.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

default_recipe=$(make -n selfhost ENV_FILE=/dev/null)
[[ "$default_recipe" == *"up -d --no-build"* ]] || { echo 'selfhost must use CI images'; exit 1; }
[[ "$default_recipe" != *"up -d --build"* ]] || { echo 'selfhost must not compile'; exit 1; }

for service in backend frontend; do
  block=$(awk -v service="$service" '$0 == "  " service ":" { active=1; next } active && /^  [a-z]/ { exit } active { print }' docker-compose.selfhost.yml)
  [[ "$block" == *'pull_policy: always'* ]] || { echo "$service must pull CI images"; exit 1; }
  [[ "$block" != *'context: .'* ]] || { echo "$service must not compile"; exit 1; }
  [[ "$block" == *'ghcr.io/lsiten/'* ]] || { echo "$service must use fork images"; exit 1; }
done

official_recipe=$(make -n selfhost-images ENV_FILE=/dev/null)
[[ "$official_recipe" == *'docker-compose.selfhost.images.yml up -d --no-build'* ]]
[[ "$official_recipe" == *'docker-compose.selfhost.images.yml pull'* ]]
grep -Fq 'COPY server/migrations/ ./migrations/' Dockerfile
grep -Fq './migrate up' docker/entrypoint.sh
build_recipe=$(make -n selfhost-build ENV_FILE=/dev/null)
[[ "$build_recipe" == *'up -d --build'* ]]
grep -Fq 'pull_policy: build' docker-compose.selfhost.build.yml
echo 'PASS: CI-image deployment and explicit local-build route'
