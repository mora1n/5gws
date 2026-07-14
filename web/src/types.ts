export interface Exit { name: string; type: 'direct' | 'shadowsocks-rust'; fwmark: number; server: string; server_port: number; method: string; password: string; username: string; listen_address: string; listen_port: number; tcp: boolean | null; udp: boolean | null; timeout_seconds: number }
export interface DNSPool { name: string; probe_domain: string; upstreams: string[] }
export interface Rule { name: string; exit: string; dns_pool: string; domain?: string[]; domain_suffix?: string[]; domain_keyword?: string[]; domain_regex?: string[]; ip_cidr?: string[]; rule_set?: string[] }
export interface ImportRule { name: string; type: string; path: string; url: string; format: string; exit: string; dns_pool: string }
export interface RuleFile { imports: ImportRule[] | null; rules: Rule[] | null }
export interface Bundle {
  config: {
    system: { config_dir: string; state_dir: string; run_dir: string; user: string }
    panel: { listen: string; allowed_cidrs: string[] }
    network: { gateway_ip: string; internal_cidr: string; ingress_iface: string; http_redirect_port: number; https_redirect_port: number; quic_redirect_port: number; tcp_redirect_port: number; haproxy_max_connections: number; quic_policy: string; encrypted_dns_policy: string }
    routing: { fallback_exit: string }
    dns: { binary: string; dot_domain: string; listen_udp: string; listen_tcp: string; listen_dot: string; listen_public_dot: string; backend_resolvers: string[]; cert_dir: string; cert_file: string; key_file: string; cache_size: number; upstreams_cn: string[]; upstreams_overseas_private: string[]; upstreams_overseas_public: string[]; custom_pools: DNSPool[] }
    logging: { level: string; access: boolean | null }
    ios: { enabled: boolean; listen: string; base_url: string; organization: string; profile_identifier: string }
    exits: Exit[]
  }
  rules: RuleFile
  resolved_rules?: Rule[] | null
}
export interface Dashboard { version: string; active_revision: number; rules: number; processes: { name: string; pid: number }[] }
export interface ApplyOperation { id: string; status: 'queued' | 'running' | 'succeeded' | 'failed'; changed: boolean; revision_id: number; rule_count: number; warnings: unknown[] | null; error?: string; queued_at: string; started_at?: string; finished_at?: string }
export interface Metric { timestamp: number; process_count: number; rss_bytes: number; tcp_connections: number; rx_bytes: number; tx_bytes: number; interface: string; dns_ok: boolean; dns_latency_ms: number }
export interface DNSDiagnostic { pool: string; upstream: string; protocol: string; status: string; latency_ms: number; answers?: string[]; error?: string }
export interface ExitDiagnostic { name: string; type: string; status: string; upstream?: string; upstream_status?: string; upstream_latency_ms?: number; egress_status: string; egress_ip?: string; egress_latency_ms?: number; error?: string }
export interface DOTDiagnostic { domain: string; listen: string; status: string; latency_ms?: number; certificate_status: string; expires_at?: string; days_remaining?: number; domain_match: boolean; error?: string }
export interface Diagnostics { checked_at: string; dns?: DNSDiagnostic[]; exits?: ExitDiagnostic[]; dot?: DOTDiagnostic }
export interface MatcherSummary { label: string; count: number; samples: string[] }
export interface ActiveRuleSummary { name: string; target: string; matchers: MatcherSummary[] }
export interface ActiveRuleGroup { key: string; title: string; rule_count: number; matcher_count: number; rules: ActiveRuleSummary[] }
export interface ActiveRules { revision_id: number; active_at: string; rule_count: number; matcher_count: number; groups: ActiveRuleGroup[] }
export interface IOSProfile { enabled: boolean; profile_url?: string; profile_qr?: string }
