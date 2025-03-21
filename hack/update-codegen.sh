#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Go to the root of the repository
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${SCRIPT_ROOT}"

# Ensure we have controller-gen
CONTROLLER_GEN_VERSION=v0.13.0
if ! [ -x "$(command -v controller-gen)" ]; then
    echo "Installing controller-gen ${CONTROLLER_GEN_VERSION}"
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}
fi

# Explicitly use the full path to controller-gen to avoid PATH issues
CONTROLLER_GEN=${GOPATH}/bin/controller-gen
if [ ! -f "$CONTROLLER_GEN" ]; then
    CONTROLLER_GEN=$(go env GOPATH)/bin/controller-gen
    if [ ! -f "$CONTROLLER_GEN" ]; then
        echo "Cannot find controller-gen in PATH or GOPATH/bin"
        exit 1
    fi
fi

# Skip deep-copy generation as we've manually provided it
echo "Generating CRD manifests..."
${CONTROLLER_GEN} crd:trivialVersions=true paths="./api/..." output:crd:artifacts:config=config/crd/bases
