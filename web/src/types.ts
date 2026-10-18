export interface Model {
  id: string
  name: string
}

export interface AccountInfo {
  account_id: string
  email: string
  name: string
  plan: string
  subscription_status?: 'verified_business' | 'legacy_unverified'
  business_verified_at?: string
  space: string
  space_id?: string
  space_id_short?: string
  exhausted: boolean
  permanent: boolean
  // no_workspace is true when the backend probed loadUserContent and found
  // that user_root.space_views is empty — the /ai SPA gets stuck on a
  // skeleton screen for these accounts. Dashboard treats them as
  // unusable (no click-through, sorted to the bottom, dedicated badge).
  no_workspace?: boolean
  // space_count is the raw probe result. Absent when the backend never
  // probed (fresh registration before the first refresh tick).
  space_count?: number
  workspace_checked_at?: string
  eligible?: boolean
  usage?: number
  limit?: number
  remaining?: number
  space_usage?: number
  space_limit?: number
  space_remaining?: number
  user_usage?: number
  user_limit?: number
  user_remaining?: number
  checked_at?: string
  exhausted_at?: string
  last_usage_at?: number
  models?: Model[]
  research_usage?: number
  has_premium?: boolean
  premium_balance: number | null
  premium_balance_known: boolean
  quota_status_reason?: 'premium_balance_unknown' | 'premium_balance_conflict' | 'eligibility_conflict'
  premium_usage?: number
  premium_limit?: number
  rate_limit?: {
    status: string
    window: { used: number; limit: number; window: string }
    rolling_reset_at_ms: number
    billingPeriodWindow: { used: number; limit: number; periodEndMs: number }
  }
  rate_limit_error?: string
  token_v2?: string
  registered_via?: string
}

export interface ProviderInfo {
  id: string
  display: string
  format_hint: string
  recommended_concurrency: number
  enabled: boolean
}

export type JobState = 'running' | 'done' | 'cancelled'
export type StepStatus = 'pending' | 'running' | 'ok' | 'fail'

export interface RegisterStep {
  email: string
  status: StepStatus
  message?: string
  space_id?: string
  user_id?: string
  file?: string
  started_at?: number
  ended_at?: number
}

export interface RegisterJob {
  id: string
  created_at: number
  ended_at?: number
  provider?: string
  proxy?: string
  concurrency: number
  total: number
  ok: number
  fail: number
  done: number
  state: JobState
  steps: RegisterStep[]
}

export interface JobStartResponse {
  job_id: string
  provider: string
  total: number
  concurrency: number
  proxy?: string
  retry_of?: string
}

export interface RefreshStatus {
  refreshing: boolean
  done: number
  total: number
  last_refresh_at?: string
  error?: string
}

// AccountSummary mirrors Go's proxy.AccountSummary. The backend computes
// pool-wide aggregates (counts, quota sums) so the dashboard headline
// cards don't need to download the full account list to render. These
// fields reflect ALL accounts regardless of the current ?q= filter, so
// the headline numbers stay stable while the user searches.
export interface AccountSummary {
  exhausted_only: number
  no_workspace: number
  premium_accounts: number
  research_limited: number
  total_research_usage: number
  total_remaining: number
  total_space_usage: number
  total_space_limit: number
  total_space_remaining: number
  total_user_usage: number
  total_user_limit: number
  total_user_remaining: number
  total_premium_balance: number
  premium_unknown_accounts: number
  total_premium_limit: number
}

export interface DashboardData {
  total: number
  available: number
  models: Model[]
  accounts: AccountInfo[]
  refresh?: RefreshStatus
  // Present whenever the request used pagination params. `accounts` is
  // already the page slice; use `filtered_total` to compute pagination
  // controls on the client.
  page?: number
  page_size?: number
  filtered_total?: number
  // Pool-wide aggregates over ALL accounts (independent of ?q=).
  // The backend always emits this; the optional marker keeps older
  // dev builds tolerant.
  summary?: AccountSummary
}

export interface TokenBucket {
  input: number
  output: number
  total: number
  requests?: number
}

export interface TokenDayPoint {
  date: string
  input: number
  output: number
  total: number
}

export interface TokenModelRow {
  model: string
  input: number
  output: number
  total: number
  count: number
}

export interface TokenAccountRow {
  email: string
  input: number
  output: number
  total: number
  count: number
}

export interface TokenStats {
  total: TokenBucket
  today: TokenBucket
  last_24h: TokenBucket
  by_day: TokenDayPoint[]
  top_models: TokenModelRow[]
  top_accounts: TokenAccountRow[]
  last_record_at: number
}

export interface LogCoverage {
  storage_status: 'ok' | 'degraded' | 'unavailable'
  captured_since: number
  retained_from: number
  data_complete: boolean
  retention_days: number
}

export interface RequestUsage {
  input_tokens: number | null
  output_tokens: number | null
  usage_source: 'actual' | 'estimated' | 'unknown' | 'mixed'
  reported_input_tokens: number | null
  reported_output_tokens: number | null
  estimated_input_tokens: number | null
  estimated_output_tokens: number | null
  upstream_call_count: number
  reported_calls: number
  estimated_calls: number
  unknown_calls: number
  usage_completeness: 'complete' | 'partial' | 'unknown'
}

export type RequestStatus = 'in_progress' | 'success' | 'rejected' | 'failed' | 'cancelled' | 'interrupted'
export interface RequestLog extends RequestUsage {
  schema_version: number
  request_id: string
  started_at_ms: number
  finished_at_ms: number | null
  protocol: 'anthropic_messages' | 'openai_chat' | 'openai_responses'
  requested_model: string
  resolved_model: string
  stream: boolean
  status: RequestStatus
  http_status: number
  duration_ms: number | null
  first_token_ms: number | null
  final_account_id: string | null
  attempt_count: number
  error_code: string
  partial_response: boolean
  completion_emitted: boolean
}

export interface RequestAttempt extends RequestUsage {
  request_id: string
  attempt_no: number
  account_id: string
  started_at_ms: number
  finished_at_ms: number | null
  duration_ms: number | null
  outcome: string
  error_code: string
  retry_reason: string
  cooldown_until_ms: number | null
  response_committed: boolean
  content_emitted: boolean
  tool_call_emitted: boolean
}

export interface RequestLogList extends LogCoverage {
  items: RequestLog[]
  total: number
  next_cursor: string | null
}

export interface RequestLogDetail extends LogCoverage {
  request: RequestLog
  attempts: RequestAttempt[]
}

export interface RequestMetrics extends RequestUsage {
  total_requests: number
  in_progress: number
  success: number
  rejected: number
  failed: number
  cancelled: number
  interrupted: number
  success_rate: number | null
  avg_duration_ms: number | null
  p50_duration_ms: number | null
  p95_duration_ms: number | null
}

export interface RequestAnalytics extends LogCoverage {
  from: number
  to: number
  bucket_ms: number
  overview: RequestMetrics
  timeline: Array<Pick<RequestMetrics, 'total_requests' | 'in_progress' | 'success' | 'rejected' | 'failed' | 'cancelled' | 'interrupted'> & { started_at_ms: number }>
  models: Array<RequestMetrics & { model: string }>
  accounts: Array<RequestUsage & { account_id: string; requests: number; attempts: number; success: number; failed: number; cancelled: number }>
}

export interface RoutingConfig {
  mode: 'quota' | 'round_robin'
  max_attempts: number
  max_concurrent_per_account: number
  disabled_account_ids: string[]
  retention_days: number
}

export interface RoutingMember {
  account_id: string
  enabled: boolean
  status: 'available' | 'paused' | 'exhausted' | 'cooldown' | 'invalid' | 'busy' | 'no_workspace'
  reason: string
  score_source: string
  quota_checked_at_ms?: number
  cooldown_until_ms?: number
  in_flight: number
}

export interface RoutingData extends RoutingConfig {
  total: number
  available: number
  paused: number
  exhausted: number
  cooldown: number
  invalid: number
  busy: number
  accounts: RoutingMember[]
}
