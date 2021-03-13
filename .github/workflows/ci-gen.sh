#!/usr/bin/env bash
set -eux pipefail

# Github Actions does not provide either:
#
# 1. Any way to re-use YAML.
# 2. Re-run a single job in a workflow.
#
# So this script auto-generates E2E workflows so they can be re-run.

cd $(dirname $0)

gen() {
  name=$1
  test=$2
  containerRuntimeExecutor=${3:-emissary}
  sed "s/name: E2E/name: $name/" < e2e.yaml.0 |
    sed "s/\${{matrix.test}}/$test/g" |
    sed "s/\${{matrix.containerRuntimeExecutor}}/${containerRuntimeExecutor}/g" |
    ../../hack/auto-gen-msg.sh > "$test-$containerRuntimeExecutor.yaml"
}


gen "Test API" test-api
gen "Test CLI" test-cli
gen "Test Cron" test-e2e-cron
gen "Test E2E" test-examples
gen "Test Examples" test-examples
gen "Test Executor (docker)" test-executor docker
gen "Test Executor (Emissary)" test-executor emissary
gen "Test Executor (k8sapi)" test-executor k8sapi
gen "Test Executor (kubelet)" test-executor kubelet
gen "Test Executor (pns)" test-executor pns
gen "Test Functionality " test-functional
