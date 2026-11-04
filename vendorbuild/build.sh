#!/bin/bash
# Pathvector NG vendor platform builder
# Each platform build is best-effort: a missing signing key or tool for one
# platform shouldn't fail the whole release.

cd dist/ || exit 1

# Check for lockfile
if [ -f "vendor.lock" ]; then
  exit 0
else
  touch vendor.lock
fi

failed=""
for d in ../vendorbuild/*/; do
  echo "Building for $d"
  "$d"build.sh || failed="$failed $d"
done

if [ -n "$failed" ]; then
  echo "WARNING: vendor builds skipped/failed for:$failed"
fi
exit 0
