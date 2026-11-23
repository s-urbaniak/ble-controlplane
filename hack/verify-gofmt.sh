#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
unformatted="$(find pkg cmd internal -type f -name '*.go' -not -path '*/vendor/*' -exec gofmt -l {} +)"
if [[ -n "${unformatted}" ]]; then
    echo 'Go files need formatting:' >&2
    echo "${unformatted}" >&2
    exit 1
fi
