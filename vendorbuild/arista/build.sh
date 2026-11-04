#!/bin/bash

set -e

echo "format: 1" > manifest.txt
echo "primaryRpm: $(ls pathvector*amd64.rpm)" >> manifest.txt
for f in pathvector*amd64.rpm; do echo "$f-sha1: $(sha1sum $f | cut -d " " -f 1)"; done >> manifest.txt
zip pathvector-ng-"$(git describe --tags "$(git rev-list --tags --max-count=1)" | cut -c2-)"-arista-amd64.swix manifest.txt pathvector*amd64.rpm
