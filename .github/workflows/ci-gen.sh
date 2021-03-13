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
   sed "s/name: E2E/name: $1/" < e2e.yaml.0 |
   sed "s/\${{matrix.test}}/$2/g" |
   sed "s/\${{matrix.containerRuntimeExecutor}}/$3/g" |
   ../../hack/auto-gen-msg.sh > $2-$3.yaml
}

gen "Test Executor (docker)" test-executor docker
gen "Test Executor (Emissary)" test-executor emissary
gen "Test Executor (k8sapi)" test-executor k8sapi
gen "Test Executor (kubelet)" test-executor kubelet
gen "Test Executor (pns)" test-executor pns

gen "Test API" test-api docker
gen "Test CLI" test-cli docker
gen "Test Cron Workflows" test-e2e-cron docker
gen "Test Examples" test-examples docker
gen "Test Functionality " test-functional docker
