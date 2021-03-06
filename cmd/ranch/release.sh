#!/usr/bin/env bash
set -e
set -o pipefail

indent() {
  sed -u 's/^/       /'
}

version=$(cat .goxc.json | jq -r '.PackageVersion')
minorver=$(echo $version | awk -F. '{print $1 "." $2}')

echo "releasing v${version}..."

goxc 2>&1 | indent

echo "syncing ranch-updates S3 bucket"
mkdir -p public
( ls -d public/*/ | grep -v "^public/${minorver}." | xargs rm -rf ) || true
aws-vault exec prod -- aws s3 sync --exclude "*" --include "${minorver}.*" --include "darwin-amd64.json" --include "linux-amd64.json" s3://ranch-updates.goodeggs.com/stable/ranch/ public/

echo "go-selfupdate generating bindiffs"
mkdir releases/${version}/bins
cp releases/${version}/darwin_amd64/ranch releases/${version}/bins/darwin-amd64
cp releases/${version}/linux_amd64/ranch releases/${version}/bins/linux-amd64
go-selfupdate releases/${version}/bins/ ${version}

echo "syncing ranch-updates S3 bucket"
aws-vault exec prod -- aws s3 sync --acl public-read public/ s3://ranch-updates.goodeggs.com/stable/ranch/

sha=$(shasum -a 256 releases/${version}/ranch_${version}_darwin_amd64.zip | awk '{print $1}')

cat <<-EOF

ranch v${version} released.

NOTE: you must go update the homebrew formula manually.
    source:  https://github.com/goodeggs/homebrew-delivery-eng/tree/master/Formula/ranch.rb
    version: ${version}
    sha256:  ${sha}

EOF
