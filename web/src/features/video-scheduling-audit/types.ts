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

import type { VideoHealthStat } from '@/features/channels/types'
import type {
  VideoReliability,
  VideoReliabilityEvidence,
  VideoScheduleCandidate,
  VideoSelectionPolicy,
} from '@/features/system-settings/types'

export const auditSearchSchema = z.object({
  start: z.number().nonnegative().optional(),
  end: z.number().positive().optional(),
  mode: z.enum(['on', 'shadow']).catch('on'),
  model: z.string().max(191).optional(),
  channel: z.number().int().nonnegative().optional(),
  group: z.string().max(191).optional(),
  version: z.string().max(64).optional(),
  outcome: z.string().max(32).optional(),
  resolution: z.string().max(64).optional(),
  seconds: z.number().min(0).max(86400).optional(),
  reference_video: z.number().int().nonnegative().optional(),
  reference_image: z.number().int().nonnegative().optional(),
  reference_audio: z.number().int().nonnegative().optional(),
  request_id: z.string().max(64).optional(),
  page: z.number().int().min(1).catch(1),
  page_size: z.number().int().min(1).max(100).catch(25),
})
export type AuditFilters = z.infer<typeof auditSearchSchema>
export type AuditRun = {
  id: number
  request_id: string
  started_at: number
  ended_at: number
  mode: 'on' | 'shadow'
  model_name: string
  request_group: string
  actual_group: string
  selected_channel: number
  task_pk: number | null
  task_id: string
  platform: string
  submit_attempts: number
  submit_accepted: number
  submit_rejected: number
  submit_unknown: number
  submit_cancelled: number
  submit_local: number
  request_outcome: string
  task_status: string
  terminal_class: string
  terminal_observed_at: number | null
  duration_ms: number | null
  cost_usd: number | null
  output_seconds: number | null
  resolution: string
  reference_video: number | null
  reference_image: number | null
  reference_audio: number | null
  scheduler_version: string
  config_version: string
  snapshot_complete: boolean
  data_issue: string
  assembly_error: string
}
export type AuditDecision = {
  selection_seq: number
  attempt_seq: number
  selected_at: number
  actual_group: string
  recommended: number
  selected: number
  choice_kind: string
  selection_reason?: string
  affinity_hit: boolean
  admission: string
  submit_outcome: string
  error_source: string
  status_code: number
  health_outcome: string
  candidate_count: number
  schema_version: string
  scheduler_version: string
  build_version: string
  config_version: string
  fingerprint: string
  snapshot_complete: boolean
  input_json: string
  board_json: string
  plugins_json: string
}
export type AuditDetail = {
  health_status?: string
  health_attempts?: {
    attempt_seq: number
    channel_id: number
    model: string
    started_at: number
    flow: string
    submit_outcome: string
    final_outcome: string
    attribution: string
    task_pk: number | null
    missing: boolean
  }[]
  run: AuditRun
  decisions: AuditDecision[]
  attempts: {
    attempt: number
    channel: number
    group: string
    outcome: string
    error_source: string
    status: number
    health: string
  }[]
}
export type AuditList = {
  items: AuditRun[]
  total: number
  filter: AuditFilters
  as_of: number
  audit_enabled: boolean
  retention_days: number
  data_range: { first: number | null; last: number | null }
  collection: {
    node: string
    running: boolean
    started_at: number
    pending: number
    pending_bytes: number
    oldest_at: number
    written: number
    dropped: number
    write_failures: number
    truncated: number
    last_issue: string
    first_issue_at: number
    last_issue_at: number
    coverage: 'unknown'
  }
}
export type AuditRate = {
  numerator: number
  denominator: number
  value: number | null
}
export type AuditStats = {
  reliability?: {
    supported: boolean
    reason?: string
    as_of: number
    attempts: VideoReliabilityEvidence
    generation_success: AuditRate
    channel_completion: AuditRate
    request_completion: AuditRate
    limited_share: AuditRate
    requests: number
    request_pending: number
    request_unknown: number
    request_missing: number
    request_user: number
    request_cancelled: number
    collection_write_failures: number
  }
  as_of: number
  total: number
  pending: number
  unknown: number
  cancelled: number
  missing: number
  submit_unknown: number
  submit_cancelled: number
  submit_local: number
  health_ignored: number
  health_unknown: number
  timeouts: number
  maturity_wait_ms: number
  request_success: AuditRate
  submit_acceptance: AuditRate
  generation_success: AuditRate
  health_success: AuditRate
  retry: AuditRate
  no_candidate: AuditRate
  p50_ms: number | null
  p95_ms: number | null
  duration_samples: number
  oldest_pending_ms: number | null
  cost_samples: number
  cost_missing: number
  mean_cost_usd: number | null
  selections: number
  candidates: number
  shadow_difference: AuditRate
  shadow_cost_samples: number
  mean_shadow_cost_delta: number | null
  exclusions: Record<string, number>
}
export type AuditCandidate = VideoScheduleCandidate
export type ChannelOverviewRow = {
  channel_id: number
  name: string
  /** 0: the channel no longer exists */
  status: number
  group: string
  priority: number
  weight: number
  model: string
  /** The channel prices this model now; live fields are empty otherwise. */
  scheduled: boolean
  quality: number
  cost_mode?: 'per_video' | 'per_second'
  prices?: Record<string, number>
  capacity: number
  capacity_group?: string
  group_capacity?: number
  group_in_flight?: number
  health?: {
    submit: VideoHealthStat
    gen: VideoHealthStat
    in_flight: number
    reliability?: VideoReliability
  }
  gated: boolean
  unproven: boolean
  /** weighted_v1 only */
  service?: number
  usage: {
    requests: number
    success: number
    failure: number
    mean_cost_usd: number | null
    mean_duration_ms: number | null
  }
}
export type ChannelOverview = {
  selection_policy: VideoSelectionPolicy
  price_weight: number
  quality_weight: number
  service_weight: number
  min_samples: number
  as_of: number
  rows: ChannelOverviewRow[]
}
