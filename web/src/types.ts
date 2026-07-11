export interface Exit { name: string; type: 'direct' | 'shadowsocks-rust'; fwmark: number; server: string; server_port: number; method: string; password: string; username: string; listen_address: string; listen_port: number; tcp: boolean | null; udp: boolean | null; timeout_seconds: number }
export interface Rule { name: string; exit: string; dns_pool: string; domain?: string[]; domain_suffix?: string[]; domain_keyword?: string[]; domain_regex?: string[]; ip_cidr?: string[]; rule_set?: string[] }
export interface ImportRule { name: string; type: string; path: string; url: string; format: string; exit: string; dns_pool: string }
export interface Bundle {
  config: {
    system: { config_dir: string; state_dir: string; run_dir: string; user: string }
    panel: { listen: string; allowed_cidrs: string[] }
    network: { gateway_ip: string; internal_cidr: string; ingress_iface: string; http_redirect_port: number; https_redirect_port: number; quic_redirect_port: number; tcp_redirect_port: number; haproxy_max_connections: number; quic_policy: string; encrypted_dns_policy: string }
    routing: { fallback_exit: string }
    dns: { binary: string; dot_domain: string; listen_udp: string; listen_tcp: string; listen_dot: string; listen_public_dot: string; backend_resolvers: string[]; cert_dir: string; cert_file: string; key_file: string; cache_size: number; upstreams_cn: string[]; upstreams_overseas_private: string[]; upstreams_overseas_public: string[] }
    logging: { level: string; access: boolean | null }
    ios: { enabled: boolean; listen: string; base_url: string; organization: string; profile_identifier: string }
    exits: Exit[]
  }
  rules: { imports: ImportRule[] | null; rules: Rule[] | null }
  resolved_rules?: Rule[] | null
}
export interface Revision { id: number; status: string; bundle: Bundle; error?: string; created_at: string; active_at?: string }
export interface Dashboard { version: string; active_revision: number; draft_revision: number; dirty: boolean; rules: number; processes: { name: string; pid: number }[] }
