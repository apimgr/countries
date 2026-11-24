#!/usr/bin/env bash

set -eo pipefail

echo "Checking for incompatible licenses..."

command -v go-licenses >/dev/null 2>&1 || {
    echo "ERROR: go-licenses not found; run inside casjaysdev/go:latest" >&2
    exit 69
}

echo "Scanning dependencies..."
mapfile -t packages < <(go list ./... | grep -v '^github.com/apimgr/countries/third_party_licenses/')
licenses=$(go-licenses csv "${packages[@]}")
if echo "$licenses" | grep -iE 'GPL|AGPL|LGPL'; then
    echo "ERROR: Copyleft license detected!" >&2
    echo "Remove the dependency or find an alternative." >&2
    exit 1
fi

echo "All detected licenses are compatible."
echo "Generating license report..."
go-licenses csv "${packages[@]}" > licenses.csv
# These tagged modules have upstream license declarations but omit license
# files from their module archives, so their attribution is maintained in LICENSE.md.
license_tmp_root="${TMPDIR:-/tmp}/apimgr"
mkdir -p "$license_tmp_root"
license_tmp_dir=$(mktemp -d "$license_tmp_root/countries-license-XXXXXX")
chmod 700 "$license_tmp_dir"
trap 'rm -rf "$license_tmp_dir"' EXIT
staged_licenses="$license_tmp_dir/third_party_licenses"
go-licenses save "${packages[@]}" \
    --ignore=github.com/alibabacloud-go \
    --ignore=modernc.org/mathutil \
    --save_path="$staged_licenses"
# License sources occasionally use a .go filename; keep them as text so Go
# package discovery does not treat the generated attribution directory as code.
while IFS= read -r -d '' license_source; do
    mv "$license_source" "${license_source%.go}.txt"
done < <(find "$staged_licenses" -type f -name '*.go' -print0)
# Keep license notices only; go-licenses may copy a full module tree when a
# license is declared in a source file rather than a conventional license file.
while IFS= read -r -d '' license_file; do
    case "$(basename "$license_file")" in
        *.go) rm -f "$license_file" ;;
        .generated-by-verify-licenses|*[Ll][Ii][Cc][Ee][Nn][Cc][Ee]*|*[Cc][Oo][Pp][Yy][Ii][Nn][Gg]*|*[Nn][Oo][Tt][Ii][Cc][Ee]*|*[Cc][Oo][Pp][Yy][Rr][Ii][Gg][Hh][Tt]*|*[Pp][Aa][Tt][Ee][Nn][Tt]*) ;;
        *) rm -f "$license_file" ;;
    esac
done < <(find "$staged_licenses" -type f -print0)
mkdir -p third_party_licenses
if [[ ! -e third_party_licenses/.generated-by-verify-licenses ]]; then
    echo "ERROR: third_party_licenses exists without this script's generated marker; refusing to replace it" >&2
    exit 1
fi
find third_party_licenses -mindepth 1 -type f ! -name '.generated-by-verify-licenses' -delete
find third_party_licenses -mindepth 1 -depth -type d -empty -delete
cp -a "$staged_licenses"/. third_party_licenses/
touch third_party_licenses/.generated-by-verify-licenses
echo "License report saved to licenses.csv and third_party_licenses/."
