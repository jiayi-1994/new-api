/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { EXCLUDED_GROUPS, FILTER_ALL, QUOTA_TYPE_VALUES } from '../constants'
import type { PricingModel } from '../types'

// ----------------------------------------------------------------------------
// Model Helper Utilities
// ----------------------------------------------------------------------------

/**
 * Get available groups for a model
 */
export function getAvailableGroups(
  model: PricingModel,
  usableGroup: Record<string, { desc: string; ratio: number }>
): string[] {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []

  return Object.keys(usableGroup)
    .filter((g) => !EXCLUDED_GROUPS.includes(g))
    .filter((g) => modelEnableGroups.includes(g))
}

/**
 * Read a configured group ratio while preserving valid zero ratios.
 */
export function getConfiguredGroupRatio(
  groupRatio: Record<string, number>,
  group: string
): number {
  const ratio = groupRatio[group]
  return typeof ratio === 'number' && Number.isFinite(ratio) ? ratio : 1
}

/**
 * Resolve the group ratio used by model square summary prices.
 *
 * When no specific group is selected, the model square shows the best price
 * available to the viewer. When a group filter is active, it shows that
 * group's price instead.
 */
export function getDisplayGroupRatio(
  model: PricingModel,
  selectedGroup?: string
): number {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  const groupRatio = model.group_ratio || {}

  if (
    selectedGroup &&
    selectedGroup !== FILTER_ALL &&
    modelEnableGroups.includes(selectedGroup)
  ) {
    return getConfiguredGroupRatio(groupRatio, selectedGroup)
  }

  if (modelEnableGroups.length === 0) {
    return 1
  }

  let minRatio = Number.POSITIVE_INFINITY

  for (const group of modelEnableGroups) {
    const ratio = groupRatio[group]
    if (
      typeof ratio === 'number' &&
      Number.isFinite(ratio) &&
      ratio < minRatio
    ) {
      minRatio = ratio
    }
  }

  return minRatio === Number.POSITIVE_INFINITY ? 1 : minRatio
}

/**
 * Replace model placeholder in endpoint path
 */
export function replaceModelInPath(path: string, modelName: string): string {
  return path.replaceAll('{model}', modelName)
}

/**
 * Check if model is token-based pricing
 */
export function isTokenBasedModel(model: PricingModel): boolean {
  return model.quota_type === QUOTA_TYPE_VALUES.TOKEN
}

/**
 * Per-second resolution prices sorted by resolution, ignoring any non-positive
 * or non-finite value the backend may still carry for legacy configurations.
 */
export function getResolutionPriceEntries(
  model: PricingModel
): Array<[string, number]> {
  // 按有效高度排序，否则 localeCompare 会把 "4k" 排在 "720p" 前面
  const height = (value: string) => {
    const numeric = Number.parseInt(value, 10)
    if (!Number.isFinite(numeric)) return 0
    return value.endsWith('k') ? numeric * 540 : numeric
  }
  return Object.entries(model.resolution_prices ?? {})
    .filter(([, price]) => Number.isFinite(price) && price > 0)
    .sort(
      ([left], [right]) =>
        height(left) - height(right) || left.localeCompare(right)
    )
}

export function getMinimumResolutionPrice(model: PricingModel): number | null {
  const prices = getResolutionPriceEntries(model).map(([, price]) => price)
  return prices.length === 0 ? null : Math.min(...prices)
}

/**
 * Resolution-priced models are always charged per second, regardless of any
 * stale legacy task_billing_mode still stored for the same model.
 */
export function isResolutionPricedModel(model: PricingModel): boolean {
  return getResolutionPriceEntries(model).length > 0
}

export function isPerSecondBilledModel(model: PricingModel): boolean {
  return (
    isResolutionPricedModel(model) || model.task_billing_mode === 'per_second'
  )
}
