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
export type SystemOption = {
  key: string
  value: string
}

export type SystemOptionKey = string

export type SystemOptionsResponse = {
  success: boolean
  message: string
  data: SystemOption[]
}

export type UpdateOptionRequest = {
  key: string
  value: string | boolean | number
}

export type UpdateOptionResponse = {
  success: boolean
  message: string
}

export interface PasskeyDomainChange {
  rp_id: string
  legacy_rp_ids: string
  origins: string
  previous_rp_id: string
  effective_rp_id: string
  removed_rp_ids: string[]
  affected_credentials: number
  unknown_credentials: number
  confirmation_required: boolean
  removal_confirmation: string
}

export interface UpdatePasskeyDomainsRequest {
  rp_id: string
  legacy_rp_ids: string
  origins: string
  preview: boolean
  removal_confirmation?: string
}

export interface UpdatePasskeyDomainsResponse extends UpdateOptionResponse {
  code?: string
  data: PasskeyDomainChange
}

export type ConfirmPaymentComplianceResponse = {
  success: boolean
  message: string
  data?: {
    confirmed: boolean
    terms_version: string
    confirmed_at: number
    confirmed_by: number
  }
}

export type SystemTaskStatus = 'pending' | 'running' | 'succeeded' | 'failed'

export type SystemTask<
  TPayload = Record<string, unknown>,
  TState = Record<string, unknown>,
  TResult = Record<string, unknown>,
> = {
  id: number
  task_id: string
  type: string
  status: SystemTaskStatus
  active_key?: string
  payload?: TPayload
  state?: TState
  result?: TResult
  error?: string
  locked_by?: string
  locked_until?: number
  created_at: number
  updated_at: number
}

export type LogCleanupTaskPayload = {
  target_timestamp: number
  batch_size: number
}

export type LogCleanupTaskState = {
  total: number
  processed: number
  progress: number
  remaining: number
}

export type LogCleanupTaskResult = {
  deleted_count: number
}

export type LogCleanupTask = SystemTask<
  LogCleanupTaskPayload,
  LogCleanupTaskState,
  LogCleanupTaskResult
>

export type SystemTaskResponse<TTask = SystemTask | null> = {
  success: boolean
  message: string
  data?: TTask
}

export type SystemTaskListResponse = {
  success: boolean
  message: string
  data?: SystemTask[]
  total: number
}

export type SystemTaskFilters = {
  type?: string
  status?: SystemTaskStatus | ''
  scope?: 'active' | 'history'
  offset?: number
}

export type SiteSettings = {
  Notice: string
  SystemName: string
  Logo: string
  Footer: string
  About: string
  HomePageContent: string
  ServerAddress: string
  TaskPublicAddress: string
  'general_setting.docs_link': string
  'legal.user_agreement': string
  'legal.privacy_policy': string
  HeaderNavModules: string
  SidebarModulesAdmin: string
}

export type AuthSettings = {
  PasswordLoginEnabled: boolean
  PasswordRegisterEnabled: boolean
  EmailVerificationEnabled: boolean
  RegisterEnabled: boolean
  EmailDomainRestrictionEnabled: boolean
  EmailAliasRestrictionEnabled: boolean
  EmailDomainWhitelist: string
  ServerAddress: string
  GitHubOAuthEnabled: boolean
  GitHubClientId: string
  GitHubClientSecret: string
  'discord.enabled': boolean
  'discord.client_id': string
  'discord.client_secret': string
  'oidc.enabled': boolean
  'oidc.display_name': string
  'oidc.client_id': string
  'oidc.client_secret': string
  'oidc.well_known': string
  'oidc.authorization_endpoint': string
  'oidc.token_endpoint': string
  'oidc.user_info_endpoint': string
  TelegramOAuthEnabled: boolean
  'telegram.client_id': string
  'telegram.client_secret': string
  TelegramBotToken: string
  TelegramBotName: string
  LinuxDOOAuthEnabled: boolean
  LinuxDOClientId: string
  LinuxDOClientSecret: string
  LinuxDOMinimumTrustLevel: string
  WeChatAuthEnabled: boolean
  WeChatServerAddress: string
  WeChatServerToken: string
  WeChatAccountQRCodeImageURL: string
  TurnstileCheckEnabled: boolean
  TurnstileSiteKey: string
  TurnstileSecretKey: string
  'passkey.enabled': boolean
  'passkey.rp_display_name': string
  'passkey.rp_id': string
  'passkey.legacy_rp_ids': string
  'passkey.origins': string
  'passkey.allow_insecure_origin': boolean
  'passkey.user_verification': 'required' | 'preferred' | 'discouraged'
  'passkey.attachment_preference': '' | 'platform' | 'cross-platform'
}

export type ContentSettings = {
  'console_setting.api_info': string
  'console_setting.announcements': string
  'console_setting.faq': string
  'console_setting.uptime_kuma_groups': string
  'console_setting.api_info_enabled': boolean
  'console_setting.announcements_enabled': boolean
  'console_setting.faq_enabled': boolean
  'console_setting.uptime_kuma_enabled': boolean
  DataExportEnabled: boolean
  DataExportDefaultTime: string
  DataExportInterval: number
  Chats: string
  DrawingEnabled: boolean
  MjNotifyEnabled: boolean
  MjAccountFilterEnabled: boolean
  MjForwardUrlEnabled: boolean
  MjModeClearEnabled: boolean
  MjActionCheckSuccessEnabled: boolean
}

export type ModelSettings = {
  'global.pass_through_request_enabled': boolean
  'global.thinking_model_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
  'gemini.safety_settings': string
  'gemini.version_settings': string
  'gemini.supported_imagine_models': string
  'gemini.thinking_adapter_enabled': boolean
  'gemini.thinking_adapter_budget_tokens_percentage': number
  'gemini.function_call_thought_signature_enabled': boolean
  'gemini.remove_function_response_id_enabled': boolean
  'claude.model_headers_settings': string
  'claude.default_max_tokens': string
  'claude.thinking_adapter_enabled': boolean
  'claude.thinking_adapter_budget_tokens_percentage': number
  'grok.violation_deduction_enabled': boolean
  'grok.violation_deduction_amount': number
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  'billing_setting.billing_mode': string
  'billing_setting.billing_expr': string
  'billing_setting.plugin_billing_expr': string
  'tool_price_setting.prices': string
  TopupGroupRatio: string
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  MaxTokenAutoGroups: number
  DefaultUseAutoGroup: boolean
  'group_ratio_setting.group_special_usable_group': string
  'model_deployment.ionet.api_key': string
  'model_deployment.ionet.enabled': boolean
}

export type BillingSettings = {
  'billing_setting.video_sales': string
  QuotaForNewUser: number
  QuotaForInviter: number
  QuotaForInvitee: number
  TopUpLink: string
  'quota_setting.enable_free_model_pre_consume': boolean
  'quota_setting.trust_quota_usd': number
  'quota_setting.pre_consume_multiplier': number
  QuotaPerUnit: number
  USDExchangeRate: number
  'general_setting.quota_display_type': string
  'general_setting.custom_currency_symbol': string
  'general_setting.custom_currency_exchange_rate': number
  DisplayInCurrencyEnabled: boolean
  DisplayTokenStatEnabled: boolean
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  'billing_setting.billing_mode': string
  'billing_setting.billing_expr': string
  'billing_setting.plugin_billing_expr': string
  'tool_price_setting.prices': string
  TopupGroupRatio: string
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  MaxTokenAutoGroups: number
  DefaultUseAutoGroup: boolean
  'group_ratio_setting.group_special_usable_group': string
  PayAddress: string
  EpayId: string
  EpayKey: string
  Price: number
  MinTopUp: number
  CustomCallbackAddress: string
  PayMethods: string
  'payment_setting.amount_options': string
  'payment_setting.amount_discount': string
  'payment_setting.compliance_confirmed': boolean
  'payment_setting.compliance_terms_version': string
  'payment_setting.compliance_confirmed_at': number
  'payment_setting.compliance_confirmed_by': number
  'payment_setting.compliance_confirmed_ip': string
  StripeApiSecret: string
  StripeWebhookSecret: string
  StripePriceId: string
  StripeUnitPrice: number
  StripeMinTopUp: number
  StripePromotionCodesEnabled: boolean
  CreemApiKey: string
  CreemWebhookSecret: string
  CreemTestMode: boolean
  CreemProducts: string
  WaffoEnabled: boolean
  WaffoApiKey: string
  WaffoPrivateKey: string
  WaffoPublicCert: string
  WaffoSandboxPublicCert: string
  WaffoSandboxApiKey: string
  WaffoSandboxPrivateKey: string
  WaffoSandbox: boolean
  WaffoMerchantId: string
  WaffoCurrency: string
  WaffoUnitPrice: number
  WaffoMinTopUp: number
  WaffoNotifyUrl: string
  WaffoReturnUrl: string
  WaffoPayMethods: string
  WaffoPancakeMerchantID: string
  WaffoPancakePrivateKey: string
  WaffoPancakeReturnURL: string
  // Bound by the operator through the catalog flow in the admin Pancake
  // section (saved via /api/option/waffo-pancake/save).
  WaffoPancakeStoreID: string
  WaffoPancakeProductID: string
  'checkin_setting.enabled': boolean
  'checkin_setting.min_quota': number
  'checkin_setting.max_quota': number
}

export type OperationsSettings = {
  DefaultCollapseSidebar: boolean
  DemoSiteEnabled: boolean
  SelfUseModeEnabled: boolean
  QuotaRemindThreshold: string
  SMTPServer: string
  SMTPPort: string
  SMTPAccount: string
  SMTPFrom: string
  SMTPToken: string
  SMTPSSLEnabled: boolean
  SMTPStartTLSEnabled: boolean
  SMTPInsecureSkipVerify: boolean
  SMTPForceAuthLogin: boolean
  WorkerUrl: string
  WorkerValidKey: string
  WorkerAllowHttpImageRequestEnabled: boolean
  LogConsumeEnabled: boolean
  'performance_setting.disk_cache_enabled': boolean
  'performance_setting.disk_cache_threshold_mb': number
  'performance_setting.disk_cache_max_size_mb': number
  'performance_setting.disk_cache_path': string
  'performance_setting.monitor_enabled': boolean
  'performance_setting.monitor_cpu_threshold': number
  'performance_setting.monitor_memory_threshold': number
  'performance_setting.monitor_disk_threshold': number
  'perf_metrics_setting.enabled': boolean
  'perf_metrics_setting.flush_interval': number
  'perf_metrics_setting.bucket_time': 'hour' | 'minute' | '5min'
  'perf_metrics_setting.retention_days': number
} & VideoSchedulingOptionValues

// ============================================================================
// Video scheduling (option keys video_scheduling_setting.<field>)
// ============================================================================

export type VideoSchedulingMode = 'off' | 'shadow' | 'on'
export type VideoSelectionPolicy = 'weighted_v1' | 'stability_cost_v2'

export type VideoReliabilityEvidence = {
  version: number
  source: 'window' | 'cold_start' | 'revalidation' | 'recovery'
  batch_start: number
  batch_end: number
  window_seconds: number
  as_of: number
  validated_at: number
  expires_at: number
  submitted: number
  accepted: number
  succeeded: number
  rejected: number
  generation_failed: number
  user: number
  cancelled: number
  pending: number
  unknown: number
  missing: number
}

export type VideoReliability = {
  version: number
  model: string
  state: 'unverified' | 'normal' | 'blocked' | 'recovering'
  state_version: number
  state_revision: number
  validation_round: number
  probe_failures: number
  reason: string
  integrity: 'complete' | 'uncertain' | 'unavailable'
  blocked_at: number
  recovery_started: number
  recovery_expires: number
  validation_started: number
  validation_expires: number
  last_validation_at: number
  qualification: VideoReliabilityEvidence | null
  current: VideoReliabilityEvidence | null
  recovery: VideoReliabilityEvidence | null
}

/** Global video scheduling setting as the backend stores it. */
export type VideoSchedulingSetting = {
  mode: VideoSchedulingMode
  selection_policy?: VideoSelectionPolicy
  min_margin_rate?: number
  min_overall_rate?: number
  stability_tolerance?: number
  qualification_ttl_seconds?: number
  validation_period_seconds?: number
  audit_enabled: boolean
  audit_retention_days: number
  models: string[]
  price_weight: number
  quality_weight: number
  service_weight: number
  min_submit_rate: number
  min_gen_rate: number
  min_samples: number
  window_seconds: number
  explore_share: number
  explore_max_in_flight: number
  probe_ratio: number
  probe_cooldown_sec: number
  probe_max_in_flight: number
  unknown_sell_policy: 'exclude' | 'relative'
  max_cost_to_sell_ratio: number
  tie_epsilon: number
  capacity_groups: Record<string, number>
}

/** Option values; capacity_groups stays a JSON string because options are flat. */
export type VideoSchedulingOptionValues = {
  [K in keyof VideoSchedulingSetting as `video_scheduling_setting.${K}`]: K extends 'capacity_groups'
    ? string
    : VideoSchedulingSetting[K]
}

export type VideoScheduleSimulateRequest = {
  group: string
  user_group?: string
  entry: 'protocol' | 'native'
  protocol?: string
  path?: string
  plugin_key?: string
  request_body: unknown
  health_override?: Record<string, unknown>
  inflight_override?: Record<string, unknown>
  slot_override?: Record<string, number>
  config_snapshot?: VideoSchedulingSetting
  seed?: number
  now?: string
}

export type VideoScheduleSpec = {
  output_seconds?: number
  input_video_seconds?: number
  seconds_kind?: string
  tier?: string
  references: Partial<Record<'video' | 'image' | 'audio' | 'frame', number>>
  missing?: string[]
}

export type VideoScheduleReferenceLine = {
  kind: string
  mode?: string
  tier?: string
  quantity?: number
  value?: number
  usd: number
}

/** One board row. Cost fields are absent for an invalid quote and must not render as 0. */
export type VideoScheduleCandidate = {
  id: number
  name: string
  plugin?: string
  mapped_model?: string
  spec?: VideoScheduleSpec
  tier?: string
  cost_usd?: number
  base_cost_usd?: number
  reference_cost_usd?: number
  references?: VideoScheduleReferenceLine[]
  sell_kind?: 'known' | 'free' | 'unknown'
  /** Omitted when zero, which a free sell price is. */
  sell_usd?: number
  sell_estimated?: boolean
  p: number
  q: number
  s: number
  total: number
  unproven?: boolean
  excluded?: string
  reliability?: VideoReliability
  estimated_margin?: number
  best_generation_rate?: number
  validation_slots_held?: number
  validation_slot_limit?: number
}

export type VideoScheduleSimulation = {
  candidates: VideoScheduleCandidate[]
  decision: { takeover: boolean; shadow: boolean; reason?: string }
  group: string
  model: string
  group_ratio: number
  recommended: number
  probe: boolean
  explore: boolean
  selection_policy?: VideoSelectionPolicy
  flow?: string
  selection_reason?: string
  slot_occupancy?: Record<number, number>
  validation_limits?: { explore: number; recover: number }
  now: string
  seed: number
  fingerprint: string
  segments: Record<string, string>
}

export type SecuritySettings = {
  ModelRequestRateLimitEnabled: boolean
  ModelRequestRateLimitCount: number
  ModelRequestRateLimitSuccessCount: number
  ModelRequestRateLimitDurationMinutes: number
  ModelRequestRateLimitGroup: string
  CheckSensitiveEnabled: boolean
  CheckSensitiveOnPromptEnabled: boolean
  SensitiveWords: string
  'fetch_setting.enable_ssrf_protection': boolean
  'fetch_setting.allow_private_ip': boolean
  'fetch_setting.domain_filter_mode': boolean
  'fetch_setting.ip_filter_mode': boolean
  'fetch_setting.domain_list': string[]
  'fetch_setting.ip_list': string[]
  'fetch_setting.allowed_ports': number[]
  'fetch_setting.apply_ip_filter_for_domain': boolean
  'token_setting.max_user_tokens': number
}

export type UpstreamChannel = {
  id: number
  name: string
  base_url: string
  status: number
  type?: number
}

export type RatioType =
  | 'model_ratio'
  | 'completion_ratio'
  | 'cache_ratio'
  | 'create_cache_ratio'
  | 'image_ratio'
  | 'audio_ratio'
  | 'audio_completion_ratio'
  | 'model_price'
  | 'billing_mode'
  | 'billing_expr'

export type RatioDifference = {
  current: number | string | null
  upstreams: Record<string, number | string | 'same'>
  confidence: Record<string, boolean>
}

export type DifferencesMap = Record<
  string,
  Partial<Record<RatioType, RatioDifference>>
>

export type PricingSyncValues = Partial<Record<RatioType, number | string>>
export type PricingSyncModels = Record<
  string,
  { current: PricingSyncValues; upstreams: Record<string, PricingSyncValues> }
>

export type UpstreamChannelsResponse = {
  success: boolean
  message: string
  data: UpstreamChannel[]
}

export type UpstreamConfig = {
  id: number
  name: string
  base_url: string
  endpoint: string
}

export type FetchUpstreamRatiosRequest = {
  upstreams: UpstreamConfig[]
  timeout: number
}

export type TestResult = {
  name: string
  status: 'success' | 'error'
  error?: string
}

export type UpstreamRatiosResponse = {
  success: boolean
  message: string
  data: {
    differences: DifferencesMap
    prices: PricingSyncModels
    test_results: TestResult[]
  }
}
