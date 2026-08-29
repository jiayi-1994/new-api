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
import { safeJsonParse } from '../utils/json-parser'

/** 单个模型的「分辨率 -> 每秒单价」映射 */
export type VideoResolutionPriceMap = Record<string, number>

/** VideoResolutionPrice 选项的完整文档：模型名 -> 分辨率价格表 */
export type VideoResolutionPriceOption = Record<string, VideoResolutionPriceMap>

export type VideoResolutionPriceRow = {
  id: number
  resolution: string
  price: string
}

export type VideoResolutionPriceRowErrors = {
  resolution?: 'required' | 'invalid' | 'duplicate'
  price?: 'required' | 'invalid'
}

export type VideoResolutionPriceValidation = {
  /** 校验全部通过时为规范化后的价格表，否则为 null */
  prices: VideoResolutionPriceMap | null
  errorsByRowId: Record<number, VideoResolutionPriceRowErrors>
}

export const VIDEO_RESOLUTION_PRICE_OPTION_KEY = 'VideoResolutionPrice'

// 与后端 common.NormalizeVideoResolutionKey 保持一致的规范分辨率写法
const canonicalResolutionPattern = /^(?:[1-9]\d{2,4}p|[1-9]\d*k)$/

// 按有效高度排序，否则 localeCompare 会把 "4k" 排在 "720p" 前面
const resolutionHeight = (value: string) => {
  const numeric = Number.parseInt(value, 10)
  if (!Number.isFinite(numeric)) return 0
  return value.endsWith('k') ? numeric * 540 : numeric
}

const compareResolutions = (left: string, right: string) =>
  resolutionHeight(left) - resolutionHeight(right) || left.localeCompare(right)

/** 返回规范化后的分辨率，非规范写法返回空串 */
export function normalizeVideoResolutionKey(value: string): string {
  const normalized = value.trim().toLowerCase()
  return canonicalResolutionPattern.test(normalized) ? normalized : ''
}

export function validateVideoResolutionPriceRows(
  rows: VideoResolutionPriceRow[]
): VideoResolutionPriceValidation {
  const errorsByRowId: Record<number, VideoResolutionPriceRowErrors> = {}
  const prices: VideoResolutionPriceMap = {}
  const seen = new Set<string>()

  for (const row of rows) {
    const errors: VideoResolutionPriceRowErrors = {}
    const normalized = normalizeVideoResolutionKey(row.resolution)
    if (row.resolution.trim() === '') {
      errors.resolution = 'required'
    } else if (normalized === '') {
      errors.resolution = 'invalid'
    } else if (seen.has(normalized)) {
      errors.resolution = 'duplicate'
    }

    const rawPrice = row.price.trim()
    const price = Number(rawPrice)
    if (rawPrice === '') {
      errors.price = 'required'
    } else if (!Number.isFinite(price) || price <= 0) {
      errors.price = 'invalid'
    }

    if (errors.resolution !== undefined || errors.price !== undefined) {
      errorsByRowId[row.id] = errors
      continue
    }
    seen.add(normalized)
    prices[normalized] = price
  }

  if (Object.keys(errorsByRowId).length > 0) {
    return { prices: null, errorsByRowId }
  }
  return { prices, errorsByRowId }
}

/** 只保留可解析为有限正数的条目，忽略后端/旧数据里的异常值 */
export function sanitizeVideoResolutionPriceMap(
  value: unknown
): VideoResolutionPriceMap {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {}
  }
  const sanitized: VideoResolutionPriceMap = {}
  for (const [resolution, price] of Object.entries(
    value as Record<string, unknown>
  )) {
    const normalized = normalizeVideoResolutionKey(resolution)
    if (normalized === '' || typeof price !== 'number') continue
    if (!Number.isFinite(price) || price <= 0) continue
    sanitized[normalized] = price
  }
  return sanitized
}

export function parseVideoResolutionPriceOption(
  raw?: string
): VideoResolutionPriceOption {
  const parsed = safeJsonParse<Record<string, unknown>>(raw || '{}', {
    fallback: {},
    context: 'video resolution prices',
  })
  const option: VideoResolutionPriceOption = {}
  for (const [modelName, prices] of Object.entries(parsed)) {
    const sanitized = sanitizeVideoResolutionPriceMap(prices)
    if (Object.keys(sanitized).length === 0) continue
    option[modelName] = sanitized
  }
  return option
}

/** 按分辨率数值排序，保证序列化结果稳定可比较 */
export function sortVideoResolutionPriceMap(
  prices: VideoResolutionPriceMap
): VideoResolutionPriceMap {
  const sorted: VideoResolutionPriceMap = {}
  for (const resolution of Object.keys(prices).sort(compareResolutions)) {
    sorted[resolution] = prices[resolution]
  }
  return sorted
}

/** 稳定序列化：模型名与分辨率都排序，保证 diff/签名可比较 */
export function serializeVideoResolutionPriceOption(
  option: VideoResolutionPriceOption
): string {
  const sorted: VideoResolutionPriceOption = {}
  for (const modelName of Object.keys(option).sort()) {
    const prices = option[modelName]
    if (!prices || Object.keys(prices).length === 0) continue
    sorted[modelName] = sortVideoResolutionPriceMap(prices)
  }
  return JSON.stringify(sorted, null, 2)
}

export function videoResolutionPriceRows(
  prices?: VideoResolutionPriceMap
): VideoResolutionPriceRow[] {
  if (!prices) return []
  return Object.keys(prices)
    .sort(compareResolutions)
    .map((resolution, index) => ({
      id: index + 1,
      resolution,
      price: String(prices[resolution]),
    }))
}

export type VideoResolutionOptionUpdateInput = {
  /** 重命名前的模型名；与 newName 不同时会搬迁该模型的价格表 */
  oldName?: string
  newName?: string
  videoResolutionPrice: VideoResolutionPriceOption
  /** 新的价格表；省略表示保持原值，空表示删除该模型的配置 */
  prices?: VideoResolutionPriceMap
}

/**
 * buildVideoResolutionOptionUpdate 在完整的 VideoResolutionPrice 文档上应用一次
 * 新增/改名/编辑，返回单个选项更新。它绝不读取或写入 TaskBillingMode。
 */
export function buildVideoResolutionOptionUpdate(
  input: VideoResolutionOptionUpdateInput
): { key: typeof VIDEO_RESOLUTION_PRICE_OPTION_KEY; value: string } {
  const option: VideoResolutionPriceOption = {}
  for (const [modelName, prices] of Object.entries(
    input.videoResolutionPrice
  )) {
    option[modelName] = { ...prices }
  }

  const oldName = input.oldName?.trim() || ''
  const newName = input.newName?.trim() || oldName
  let prices = input.prices
  if (prices === undefined && oldName !== '') {
    prices = option[oldName]
  }
  if (oldName !== '' && oldName !== newName) {
    delete option[oldName]
  }
  if (newName !== '') {
    const sanitized = sanitizeVideoResolutionPriceMap(prices)
    if (Object.keys(sanitized).length === 0) {
      delete option[newName]
    } else {
      option[newName] = sanitized
    }
  }

  return {
    key: VIDEO_RESOLUTION_PRICE_OPTION_KEY,
    value: serializeVideoResolutionPriceOption(option),
  }
}
