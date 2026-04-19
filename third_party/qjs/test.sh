#! /bin/bash
# go test -failfast -race -v -p 1
scriptDir="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
workingDir=$PWD
mkdir -p $scriptDir/coverage
coverageFile=$scriptDir/coverage/coverage
gotestsum -f testname \
  --debug \
  -- \
  ./... \
  -v \
  -failfast \
  -race \
  -count=1 \
  -coverprofile=$coverageFile.out \
  -covermode=atomic
go tool cover -html=$coverageFile.out -o $coverageFile.html
