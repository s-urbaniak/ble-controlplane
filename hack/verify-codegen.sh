#!/usr/bin/env bash
set -o errexit -o nounset -o pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
copy="$(mktemp -d "${TMPDIR:-/tmp}/ble-controlplane-codegen.XXXXXXXX")"
trap 'rm -rf "${copy}"' EXIT

cp "${root}/go.mod" "${root}/go.sum" "${copy}/"
cp -R "${root}/pkg" "${root}/hack" "${copy}/"
bash "${copy}/hack/update-codegen.sh"
diff -ru "${root}/pkg" "${copy}/pkg"
