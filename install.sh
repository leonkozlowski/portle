#!/bin/sh
set -eu

repository="leonkozlowski/portle"
install_dir="${PORTLE_INSTALL_DIR:-$HOME/.local/bin}"
requested_version="${PORTLE_VERSION:-latest}"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "portle: unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "portle: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if [ "$requested_version" = "latest" ]; then
  release_path="latest/download"
else
  case "$requested_version" in
    v*) version="$requested_version" ;;
    *) version="v$requested_version" ;;
  esac
  release_path="download/$version"
fi

archive="portle_${os}_${arch}.tar.gz"
base_url="https://github.com/$repository/releases/$release_path"
temporary_dir="$(mktemp -d)"
trap 'rm -rf "$temporary_dir"' EXIT INT TERM

echo "Downloading $archive..."
curl -fsSL "$base_url/$archive" -o "$temporary_dir/$archive"
curl -fsSL "$base_url/checksums.txt" -o "$temporary_dir/checksums.txt"

grep "  $archive\$" "$temporary_dir/checksums.txt" > "$temporary_dir/expected.txt"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$temporary_dir" && sha256sum -c expected.txt)
elif command -v shasum >/dev/null 2>&1; then
  (cd "$temporary_dir" && shasum -a 256 -c expected.txt)
else
  echo "portle: sha256sum or shasum is required" >&2
  exit 1
fi

tar -xzf "$temporary_dir/$archive" -C "$temporary_dir" portle
mkdir -p "$install_dir"
install -m 0755 "$temporary_dir/portle" "$install_dir/portle"

echo "Installed portle to $install_dir/portle"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "Add $install_dir to PATH to run portle." ;;
esac
