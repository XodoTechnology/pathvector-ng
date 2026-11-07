#!/usr/bin/env bash
# Build one or more FRR releases from source.
#
# Usage:
#   tests/frr-matrix/build-frr-versions.sh [-o OUTDIR] [TAG...]
#
# Every TAG is a git tag of the FRR repository (for example frr-10.3 or
# frr-8.5.7). An FRR release may optionally be followed by "@" and a libyang
# git tag (for example "frr-10.0.5@v2.1.148") when that release only builds
# against an older libyang than the host provides — FRR's configure enforces
# only a minimum libyang, but releases up to ~10.2 use libyang-2 APIs that
# were removed in libyang 3. In versions.txt the same pairing is written as
# two whitespace-separated fields: "frr-10.0.5 v2.1.148".
#
# The resulting installation trees are written to
# OUTDIR/TAG/root (a DESTDIR-style prefix: usr/sbin, usr/bin, etc/frr, ...).
# OUTDIR defaults to the directory of this script; tests/frr-matrix/frr-*
# is ignored by git, so local builds never show up as untracked files.
#
# When no TAG is given, the default matrix in versions.txt is built.
#
# Environment:
#   FRR_REPO  git URL to clone from (default: https://github.com/FRRouting/frr.git)
#   JOBS      parallel make jobs (default: number of CPUs)
#
# Build dependencies (Debian/Ubuntu package names) are installed by the
# Dockerfile; for a manual run install the ones listed there first. FRR's
# configure flags changed across releases, so this script only passes
# --enable/--disable options that the checked-out configure actually
# supports, and only disables standalone daemons (disabling zebra
# sub-features such as rtadv breaks the zebra link).

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="$script_dir"
FRR_REPO="${FRR_REPO:-https://github.com/FRRouting/frr.git}"
JOBS="${JOBS:-$(nproc 2>/dev/null || echo 2)}"

while getopts "o:h" opt; do
  case "$opt" in
    o) out_dir="$OPTARG" ;;
    h) sed -n '2,25p' "$0"; exit 0 ;;
    *) exit 2 ;;
  esac
done
shift $((OPTIND - 1))

if [ $# -eq 0 ]; then
  # versions.txt lines are "TAG" or "TAG LIBYANG_REF" (whitespace separated).
  mapfile -t tags < <(sed -e 's/#.*//' -e '/^[[:space:]]*$/d' "$script_dir/versions.txt")
else
  tags=("$@")
fi

# Normalize each spec to "TAG@LIBYANGREF" (the @ref may be empty). Specs from
# versions.txt use a space separator; command-line specs may already use @.
normalize() {
  local spec="$1" tag ref
  if [[ "$spec" == *@* ]]; then
    echo "$spec"
  else
    # "TAG LIBYANGREF" -> "TAG@LIBYANGREF"; bare "TAG" stays as-is.
    tag="${spec%%[[:space:]]*}"
    ref="$(echo "$spec" | sed -E 's/^[^[:space:]]+[[:space:]]+//')"
    if [ -n "$ref" ] && [ "$ref" != "$spec" ]; then
      echo "$tag@$ref"
    else
      echo "$tag"
    fi
  fi
}
specs=()
for t in "${tags[@]}"; do
  specs+=("$(normalize "$t")")
done
tags=("${specs[@]}")

mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"

# Daemons we never need for the Pathvector smoke test. Only standalone
# daemons are listed; zebra sub-features (rtadv, irdp, multipath, fpm) and
# optional integrations are left at their configure defaults because
# disabling them has broken zebra builds in the past.
DISABLE_DAEMONS="babeld bfdd eigrpd fabricd isisd ldpd mgmtd nhrpd ospf6d ospfd pathd pbrd pim6d pimd ripd ripngd sharpd vrrpd watchfrr"

# Build libyang from a git tag into $1 (a real install prefix, not a
# DESTDIR) so FRR's configure finds it via pkg-config and the resulting
# shared library can be copied into the FRR DESTDIR for runtime. libyang 2.x
# uses CMake; only the runtime lib + headers + pkgconfig are needed.
build_libyang() {
  local ref="$1" dest="$2" lsrc
  lsrc="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$lsrc'" RETURN

  echo "    Building libyang $ref"
  git clone --quiet --depth 1 --branch "$ref" \
    https://github.com/CESNET/libyang.git "$lsrc/libyang"
  (
    cd "$lsrc/libyang"
    mkdir -p build && cd build
    cmake -DCMAKE_INSTALL_PREFIX="$dest" -DCMAKE_INSTALL_LIBDIR=lib \
      -DENABLE_TESTS=OFF -DENABLE_EXAMPLES=OFF ..
    make -j"$JOBS" >/dev/null
    make install >/dev/null
  )
}

build_one() {
  local spec="$1" tag libyang_ref=""
  # Split "TAG@LIBYANGREF"; a bare "TAG" uses the system libyang.
  if [[ "$spec" == *@* ]]; then
    tag="${spec%%@*}"
    libyang_ref="${spec#*@}"
  else
    tag="$spec"
  fi
  local src
  src="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$src'" RETURN

  echo "==> Building FRR $tag from $FRR_REPO${libyang_ref:+ (libyang $libyang_ref)}"
  git clone --quiet --depth 1 --branch "$tag" "$FRR_REPO" "$src"

  # When the release needs an older libyang than the host's, build that
  # libyang from source into a scratch prefix and point FRR's configure at
  # it. The shared library is copied into the FRR DESTDIR below so the
  # runtime loader (LD_LIBRARY_PATH in run-tests.sh) resolves it.
  local lyroot="" ly_pc="" ly_lib=""
  if [ -n "$libyang_ref" ]; then
    lyroot="$src/lyroot"
    mkdir -p "$lyroot"
    build_libyang "$libyang_ref" "$lyroot"
    ly_pc="$lyroot/lib/pkgconfig"
    ly_lib="$lyroot/lib"
  fi

  (
    cd "$src"
    # Release tarballs ship a generated ./configure; git checkouts do not.
    if [ ! -x ./configure ]; then
      if [ -x ./bootstrap.sh ]; then
        ./bootstrap.sh
      else
        autoreconf -i
      fi
    fi

    # Discover which --enable/--disable options this release supports so we
    # never pass a flag that does not exist yet (or was removed).
    local_flags="$(./configure --help 2>/dev/null \
      | grep -oE -- '--(enable|disable)-[a-zA-Z0-9_-]+' | sort -u)"
    has_flag() { grep -qx -- "$1" <<<"$local_flags"; }

    args=(
      --prefix=/usr
      --sysconfdir=/etc/frr
      --localstatedir=/var/run/frr
    )
    # Enable the daemons Pathvector needs. Older releases built every daemon
    # unconditionally and have no per-daemon flags; only pass --enable-X when
    # this configure actually supports it.
    for d in bgpd zebra staticd vtysh; do
      if has_flag "--enable-$d"; then
        args+=("--enable-$d")
      fi
    done
    if has_flag --disable-doc; then
      args+=(--disable-doc)
    fi
    # RPKI (rpki node + match rpki) is required by Pathvector's generated
    # config. Enable it only where the flag exists; older releases either
    # lack it entirely or enable it unconditionally.
    if has_flag --enable-rpki; then
      args+=(--enable-rpki)
    fi
    for d in $DISABLE_DAEMONS; do
      if has_flag "--disable-$d"; then
        args+=("--disable-$d")
      fi
    done

    # When a scratch libyang was built, point pkg-config (and the linker via
    # the .pc -L flag) at it so configure/make use that libyang instead of
    # the host's newer one.
    if [ -n "$ly_pc" ]; then
      export PKG_CONFIG_PATH="$ly_pc${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
      export LD_LIBRARY_PATH="$ly_lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    fi

    echo "    configure ${args[*]}"
    ./configure "${args[@]}"
    make -j"$JOBS" >/dev/null || make -j1 >/dev/null
  )

  local dest="$out_dir/$tag/root"
  mkdir -p "$dest"
  (cd "$src" && make install DESTDIR="$dest" >/dev/null)
  # Carry the scratch libyang's shared library into the FRR DESTDIR so the
  # runtime loader finds it via run-tests.sh's LD_LIBRARY_PATH (which scans
  # $root/usr/lib). Only the .so is needed at runtime; headers/pkgconfig were
  # for the build.
  if [ -n "$ly_lib" ]; then
    mkdir -p "$dest/usr/lib"
    cp -a "$ly_lib"/libyang.so* "$dest/usr/lib/" 2>/dev/null || true
  fi
  # Marker file so run-tests.sh can name the version without parsing paths.
  echo "$tag" > "$dest/FRR_VERSION"

  # Report the version the built bgpd reports, for the log.
  local vbin
  vbin="$(find "$dest" -name bgpd -type f | head -1)"
  echo "==> $tag installed to $dest (${vbin:-no bgpd found})"
}

for tag in "${tags[@]}"; do
  build_one "$tag"
done
