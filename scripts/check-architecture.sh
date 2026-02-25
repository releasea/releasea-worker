#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

MODULE_PATH="$(go list -m -f '{{.Path}}')"
VIOLATIONS_FILE="$(mktemp)"
trap 'rm -f "${VIOLATIONS_FILE}"' EXIT

forbidden_import_prefixes=(
  "${MODULE_PATH}/internal/background"
  "${MODULE_PATH}/internal/config"
  "${MODULE_PATH}/internal/common"
  "${MODULE_PATH}/internal/contracts"
  "${MODULE_PATH}/internal/deploy"
  "${MODULE_PATH}/internal/integrations"
  "${MODULE_PATH}/internal/models"
  "${MODULE_PATH}/internal/modules"
  "${MODULE_PATH}/internal/rules"
  "${MODULE_PATH}/internal/shared"
  "${MODULE_PATH}/internal/worker"
)

allowed_feature_edges=(
  "maintenance->deploys"
  "maintenance->rules"
  "workers->deploys"
  "workers->maintenance"
  "workers->rules"
)

feature_name_from_path() {
  local import_path="$1"
  local prefix="${MODULE_PATH}/internal/features/"
  if [[ "${import_path}" != "${prefix}"* ]]; then
    echo ""
    return 0
  fi
  local rest="${import_path#${prefix}}"
  echo "${rest%%/*}"
}

is_allowed_feature_edge() {
  local edge="$1"
  local allowed
  for allowed in "${allowed_feature_edges[@]}"; do
    if [[ "${allowed}" == "${edge}" ]]; then
      return 0
    fi
  done
  return 1
}

while IFS='|' read -r pkg imports_csv; do
  imports=()
  if [[ -n "${imports_csv}" ]]; then
    IFS=',' read -r -a imports <<< "${imports_csv}"
  fi
  for imp in "${imports[@]-}"; do
    [[ -z "${imp}" ]] && continue

    for forbidden_prefix in "${forbidden_import_prefixes[@]}"; do
      if [[ "${imp}" == "${forbidden_prefix}" || "${imp}" == "${forbidden_prefix}/"* ]]; then
        echo "[forbidden-import] ${pkg} -> ${imp}" >> "${VIOLATIONS_FILE}"
      fi
    done

    if [[ "${pkg}" == "${MODULE_PATH}/cmd"* && "${imp}" == "${MODULE_PATH}/internal/features/"* ]]; then
      if [[ "${imp}" != "${MODULE_PATH}/internal/features/workers/"* ]]; then
        echo "[cmd-boundary] ${pkg} must only depend on workers entrypoint (${imp})" >> "${VIOLATIONS_FILE}"
      fi
    fi

    if [[ "${pkg}" == "${MODULE_PATH}/internal/platform/"* && "${pkg}" != "${MODULE_PATH}/internal/platform" && "${imp}" == "${MODULE_PATH}/internal/features/"* ]]; then
      echo "[platform-boundary] ${pkg} must not depend on feature package ${imp}" >> "${VIOLATIONS_FILE}"
    fi

    if [[ "${pkg}" == "${MODULE_PATH}/internal/platform/"* && "${pkg}" != "${MODULE_PATH}/internal/platform" && "${imp}" == "${MODULE_PATH}/internal/platform" ]]; then
      echo "[platform-boundary] ${pkg} must not depend on root platform facade package" >> "${VIOLATIONS_FILE}"
    fi

    if [[ "${pkg}" == "${MODULE_PATH}/internal/features/"* ]]; then
      if [[ "${imp}" == "${MODULE_PATH}/internal/platform" ]]; then
        echo "[feature-boundary] ${pkg} must not depend on root platform facade package" >> "${VIOLATIONS_FILE}"
      fi
    fi

    if [[ "${pkg}" == "${MODULE_PATH}/internal/features/"* && "${imp}" == "${MODULE_PATH}/internal/features/"* ]]; then
      src_feature="$(feature_name_from_path "${pkg}")"
      dst_feature="$(feature_name_from_path "${imp}")"
      if [[ -n "${src_feature}" && -n "${dst_feature}" && "${src_feature}" != "${dst_feature}" ]]; then
        edge="${src_feature}->${dst_feature}"
        if ! is_allowed_feature_edge "${edge}"; then
          echo "[feature-dependency] forbidden edge ${edge} (${pkg} -> ${imp})" >> "${VIOLATIONS_FILE}"
        fi
      fi
    fi
  done
done < <(go list -f '{{.ImportPath}}|{{join .Imports ","}}' ./...)

if [[ -s "${VIOLATIONS_FILE}" ]]; then
  echo "ARCH CHECK FAILED"
  sort -u "${VIOLATIONS_FILE}"
  exit 1
fi

echo "Architecture checks passed."
