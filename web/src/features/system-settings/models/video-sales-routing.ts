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
import { z } from 'zod'

import { searchChannels, type TaskPluginOption } from '@/features/channels/api'
import {
  CHANNEL_TYPE_TASK_PLUGIN,
  CHANNEL_TYPE_NEW_API,
} from '@/features/channels/constants'
import type { Channel } from '@/features/channels/types'
import type { VideoSalesModel } from '@/features/pricing/types'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  asciiFoldVideoModelName,
  canonicalVideoSalesTier,
} from './video-sales-config'

const secondsSchema = z.number().int().min(1).max(3600)
const recordSchema = z.custom<Record<string, unknown>>(
  (value) =>
    value !== null && typeof value === 'object' && !Array.isArray(value)
)
const referenceTiersSchema = z.record(
  z.string(),
  z.object({
    mode: z.enum([
      'unsupported',
      'included',
      'per_request',
      'per_input',
      'per_output_second',
      'per_input_second',
      'multiplier',
    ]),
    value: z.number().finite().nonnegative().optional(),
  })
)
const costSchema = z.object({
  mode: z.enum(['per_video', 'per_second']),
  prices: z.record(z.string(), z.number().finite().nonnegative()),
  min_seconds: z.number().int().min(0).max(3600).optional(),
  max_seconds: z.number().int().min(0).max(3600).optional(),
  allowed_seconds: z.array(secondsSchema).nullish(),
  allowed_seconds_by_resolution: z
    .record(z.string(), z.array(secondsSchema).min(1))
    .nullish(),
  references: z
    .object({
      video: referenceTiersSchema.optional(),
      image: referenceTiersSchema.optional(),
      audio: referenceTiersSchema.optional(),
    })
    .nullish(),
})
const settingsSchema = z.object({
  video_scheduling: z
    .object({
      capacity_group: z.string().optional(),
      models: recordSchema.nullish(),
    })
    .nullish(),
})
const pluginSettingsSchema = z.object({
  task_plugin_key: z.string().optional(),
  task_extend_plugin_keys: z.array(z.string()).nullish(),
})

export type VideoSalesRoute = {
  channel: Channel
  plugin: string
  target: string | null
  capacityGroup: string
  issue: string | null
  uncertain: boolean
  cost: z.infer<typeof costSchema> | null
  coverage: { tier: string; seconds: number[]; price: number }[]
}

/** Load every page before reporting coverage; server LIKE case rules vary by database. */
export async function getVideoSalesChannels(): Promise<Channel[]> {
  const channels = new Map<number, Channel>()
  for (let page = 1; ; page += 1) {
    const response = requireServerSuccess(
      await searchChannels({
        p: page,
        page_size: 100,
        tag_mode: false,
        id_sort: true,
      })
    )
    if (!response.data) throw new Error('Failed to load channel routing')
    const previousSize = channels.size
    for (const channel of response.data.items) channels.set(channel.id, channel)
    if (channels.size >= response.data.total) return [...channels.values()]
    if (channels.size === previousSize) {
      throw new Error('Incomplete channel routing results')
    }
  }
}

/** Static procurement coverage only; plugin decoding and admission remain server decisions. */
export function inspectVideoSalesRoute(
  channel: Channel,
  modelName: string,
  sales: VideoSalesModel,
  plugins: TaskPluginOption[]
): VideoSalesRoute {
  const route: VideoSalesRoute = {
    channel,
    plugin: '',
    target: null,
    capacityGroup: '',
    issue: null,
    uncertain: false,
    cost: null,
    coverage: [],
  }
  try {
    const settings = settingsSchema.parse(
      JSON.parse(channel.settings || '{}')
    ).video_scheduling
    route.capacityGroup = settings?.capacity_group ?? ''
    const pluginSettings = pluginSettingsSchema.parse(
      JSON.parse(channel.setting || '{}')
    )
    const keys = new Set<string>()
    if (
      channel.type === CHANNEL_TYPE_TASK_PLUGIN ||
      channel.type === CHANNEL_TYPE_NEW_API
    ) {
      if (pluginSettings.task_plugin_key) {
        keys.add(pluginSettings.task_plugin_key)
      }
      for (const key of pluginSettings.task_extend_plugin_keys ?? []) {
        keys.add(key)
      }
    } else {
      for (const plugin of plugins) {
        if (plugin.channelTypes?.includes(channel.type)) keys.add(plugin.key)
      }
    }
    route.plugin = [...keys].join(', ')
    const executionPlugins = plugins.filter((plugin) => keys.has(plugin.key))
    const mapping = Object.fromEntries(
      z
        .array(z.tuple([z.string(), z.string()]))
        .parse(
          Object.entries(
            recordSchema.parse(JSON.parse(channel.model_mapping || '{}'))
          )
        )
    )
    const folded = asciiFoldVideoModelName(modelName)
    const starts = Object.keys(mapping)
      .sort()
      .filter(
        (key) => asciiFoldVideoModelName(key) === folded && mapping[key] !== ''
      )
    if (new Set(starts.map((key) => mapping[key])).size > 1) {
      route.issue = 'Conflicting upstream mappings'
      return route
    }
    let target = starts[0] ?? modelName
    let mapped = false
    const visited = new Set([target])
    while (
      Object.hasOwn(mapping, target) &&
      mapping[target] &&
      mapping[target] !== target
    ) {
      target = mapping[target]
      if (visited.has(target)) {
        route.issue = 'Upstream mapping contains a cycle'
        return route
      }
      visited.add(target)
      mapped = true
    }
    // These names may use host settings (modifier/legacy-alias preservation).
    // A browser cannot safely infer the server's BaseModelName fallback.
    const hasHostAlias = /@|^(?:.*\/)?(?:gpt-|o\d|claude-|gemini-)/
    if (
      (!Object.hasOwn(mapping, target) || !mapping[target]) &&
      hasHostAlias.test(target)
    ) {
      route.uncertain = true
      route.issue = 'Routing requires runtime confirmation'
    }
    if (!mapped) {
      target =
        executionPlugins
          .flatMap((plugin) => plugin.models)
          .find((name) => asciiFoldVideoModelName(name) === folded) ?? ''
    }
    route.target = target || null
    if (!executionPlugins.length) route.issue ??= 'Execution plugin unavailable'
    else if (!target) route.issue ??= 'No upstream mapping'
    else if (
      executionPlugins.every((plugin) => plugin.key === 'paipu') &&
      (target === 'paipu-video' || target.startsWith('paipu-video-'))
    ) {
      route.issue = 'Upstream mapping must name a real model'
      route.uncertain = false
    }
    const costs = settings?.models ?? {}
    if (
      Object.keys(costs).filter(
        (key) => asciiFoldVideoModelName(key) === folded
      ).length > 1
    ) {
      route.uncertain = true
      route.issue = 'Routing requires runtime confirmation'
    }
    const costKey = Object.hasOwn(costs, modelName)
      ? modelName
      : Object.keys(costs)
          .sort()
          .find((key) => asciiFoldVideoModelName(key) === folded)
    if (!costKey) {
      if (
        hasHostAlias.test(modelName) ||
        Object.keys(costs).some((key) => hasHostAlias.test(key))
      ) {
        route.uncertain = true
        route.issue = 'Routing requires runtime confirmation'
      }
      route.issue ??= 'Procurement cost is not configured'
      return route
    }
    const parsed = costSchema.safeParse(costs[costKey])
    if (
      !parsed.success ||
      !Object.keys(parsed.data.prices).length ||
      ((parsed.data.max_seconds ?? 0) > 0 &&
        (parsed.data.min_seconds ?? 0) > (parsed.data.max_seconds ?? 0))
    ) {
      route.uncertain = false
      route.issue = 'Invalid procurement configuration'
      return route
    }
    const cost = parsed.data
    route.cost = cost
    const capabilities = cost.allowed_seconds_by_resolution
    if (capabilities != null) {
      const tiers = Object.keys(capabilities).map(canonicalVideoSalesTier)
      if (
        !tiers.length ||
        tiers.includes(null) ||
        new Set(tiers).size !== tiers.length
      ) {
        route.uncertain = false
        route.issue = 'Invalid procurement configuration'
        return route
      }
    }
    if (channel.status !== 1) {
      route.uncertain = false
      route.issue = 'Channel disabled'
    }
    if (route.issue) return route
    for (const [resolution, tierSales] of Object.entries(sales.resolutions)) {
      const tier = canonicalVideoSalesTier(resolution)
      if (!tier) continue
      const price =
        cost.prices[tier] ??
        (tier === '4k' ? cost.prices['2160p'] : undefined) ??
        cost.prices['*']
      if (price === undefined) continue
      const allowed =
        capabilities?.[tier] ??
        (tier === '4k' ? capabilities?.['2160p'] : undefined)
      const seconds = tierSales.seconds.filter((value) => {
        if (capabilities != null) return allowed?.includes(value) ?? false
        return (
          value >= (cost.min_seconds ?? 0) &&
          (!(cost.max_seconds ?? 0) || value <= (cost.max_seconds ?? 0)) &&
          (!cost.allowed_seconds?.length ||
            cost.allowed_seconds.includes(value))
        )
      })
      if (seconds.length) route.coverage.push({ tier, seconds, price })
    }
    return route
  } catch {
    route.issue = 'Invalid channel routing configuration'
    return route
  }
}
