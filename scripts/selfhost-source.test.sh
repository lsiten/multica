#!/usr/bin/env bash
# Guard the fork's source-first deployment without launching containers.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

default_recipe=$(make -n selfhost ENV_FILE=/dev/null)
[[ "$default_recipe" == *"up -d --build"* ]] || { echo 'selfhost must build current source'; exit 1; }
[[ "$default_recipe" != *"Pulling official"* ]] || { echo 'selfhost must not pull upstream images'; exit 1; }

for service in backend frontend; do
  block=$(awk -v service="$service" '$0 == "  " service ":" { active=1; next } active && /^  [a-z]/ { exit } active { print }' docker-compose.selfhost.yml)
  [[ "$block" == *'pull_policy: build'* ]] || { echo "$service must rebuild on compose up"; exit 1; }
  [[ "$block" == *'context: .'* ]] || { echo "$service must use current checkout"; exit 1; }
done

official_recipe=$(make -n selfhost-images ENV_FILE=/dev/null)
[[ "$official_recipe" == *'docker-compose.selfhost.images.yml up -d --no-build'* ]]
[[ "$official_recipe" == *'docker-compose.selfhost.images.yml pull'* ]]
grep -Fq 'COPY server/migrations/ ./migrations/' Dockerfile
grep -Fq './migrate up' docker/entrypoint.sh
echo 'PASS: source-first deployment and explicit prebuilt-image route'
