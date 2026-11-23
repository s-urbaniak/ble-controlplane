#!/usr/bin/env bash
set -o errexit -o nounset -o pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root}"

generator="$(go list -m -f '{{.Dir}}' k8s.io/code-generator)"
export GOBIN="${root}/_output/codegen"
source "${generator}/kube_codegen.sh"

module="$(go list -m)"
boilerplate="${root}/hack/boilerplate.go.txt"
kube::codegen::gen_helpers --boilerplate "${boilerplate}" "${root}/pkg/apis"
kube::codegen::gen_register --boilerplate "${boilerplate}" "${root}/pkg/apis"
kube::codegen::gen_openapi \
    --output-dir "${root}/pkg/generated/openapi" \
    --output-pkg "${module}/pkg/generated/openapi" \
    --output-model-name-file zz_generated.model_name.go \
    --report-filename "${root}/hack/api-rules/violation_exceptions.list" \
    --boilerplate "${boilerplate}" \
    "${root}/pkg/apis"
kube::codegen::gen_client \
    --with-watch \
    --output-dir "${root}/pkg/generated" \
    --output-pkg "${module}/pkg/generated" \
    --boilerplate "${boilerplate}" \
    "${root}/pkg/apis"
