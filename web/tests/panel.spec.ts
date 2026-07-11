import { expect, test, type Page } from '@playwright/test'

const bundle = {
  config: {
    system: { config_dir: '/etc/5gws', state_dir: '/var/lib/5gws', run_dir: '/run/5gws', user: 'root' },
    panel: { listen: '127.0.0.1:19443', allowed_cidrs: ['127.0.0.0/8', '::1/128'] },
    network: { gateway_ip: '10.0.0.1', internal_cidr: '10.0.0.0/24', ingress_iface: 'wwan0', http_redirect_port: 18080, https_redirect_port: 18443, quic_redirect_port: 18443, tcp_redirect_port: 18082, haproxy_max_connections: 16384, quic_policy: 'reject', encrypted_dns_policy: 'reject' },
    routing: { fallback_exit: 'direct' },
    dns: { binary: '/usr/local/bin/smartdns', dot_domain: 'dns.gateway.example.net', listen_udp: '0.0.0.0:1053', listen_tcp: '0.0.0.0:1053', listen_dot: '0.0.0.0:1853', listen_public_dot: '0.0.0.0:853', backend_resolvers: ['1.1.1.1:53', '8.8.8.8:53'], cert_dir: '/var/lib/5gws/ios', cert_file: '/etc/5gws/fullchain.pem', key_file: '/etc/5gws/privkey.pem', cache_size: 32768, upstreams_cn: ['223.5.5.5', '119.29.29.29'], upstreams_overseas_private: ['1.1.1.1'], upstreams_overseas_public: ['8.8.8.8'] },
    logging: { level: 'info', access: true },
    ios: { enabled: true, listen: '0.0.0.0:8088', base_url: 'http://10.0.0.1:8088', organization: '5gws gateway operations', profile_identifier: 'dev.5gws.dot' },
    exits: [
      { name: 'direct', type: 'direct', fwmark: 0, server: '', server_port: 0, method: '', password: '', username: '', listen_address: '', listen_port: 0, tcp: true, udp: true, timeout_seconds: 300 },
      { name: 'tokyo-shadowsocks-production-long-name', type: 'shadowsocks-rust', fwmark: 0, server: 'edge.gateway.example.net', server_port: 8388, method: '2022-blake3-aes-128-gcm', password: 'secret', username: 'default', listen_address: '127.0.0.1', listen_port: 1080, tcp: true, udp: true, timeout_seconds: 300 },
    ],
  },
  rules: {
    imports: [{ name: 'category-speedtest-global', type: 'sing-box', path: '', url: 'https://raw.githubusercontent.com/example/very/long/path/category-speedtest.json', format: '', exit: 'direct', dns_pool: '' }],
    rules: [{ name: 'openai-and-related-services', exit: 'tokyo-shadowsocks-production-long-name', dns_pool: '', domain_suffix: ['openai.com', 'chatgpt.com', 'very-long-service-domain.example.net'] }],
  },
}

const revision = { id: 8, status: 'draft', bundle, created_at: '2026-07-11T12:00:00Z' }
const activeRevision = {
	...revision,
	id: 7,
	status: 'active',
	active_at: '2026-07-11T12:05:00Z',
	bundle: {
		...bundle,
		resolved_rules: [
			{ name: 'applied-openai', exit: 'tokyo-shadowsocks-production-long-name', dns_pool: '', domain_suffix: ['openai.com', 'chatgpt.com'] },
			{ name: 'applied-cn', exit: '', dns_pool: 'cn', domain_suffix: ['example.cn', 'example.com.cn', 'service.cn', 'cdn.cn', 'portal.cn', 'static.cn', 'extra.cn'] },
		],
	},
}
const pages = [
	['概览', '概览', '运行概览'], ['DNS 与网络', 'DNS', 'DNS 与网络'], ['规则', '规则', '规则'], ['出口', '出口', '出口'],
	['日志', '日志', '日志'], ['设置', '设置', '设置'],
] as const

async function mockAPI(page: Page) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    const json = (value: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
    if (path === '/api/v1/bootstrap') return json({ needs_setup: false })
    if (path === '/api/v1/me') return json({ username: 'admin' })
    if (path === '/api/v1/dashboard') return json({ version: '0.2.0', active_revision: 7, draft_revision: 8, dirty: true, rules: 10023, processes: [{ name: 'smartdns', pid: 101 }, { name: 'haproxy', pid: 102 }, { name: 'sslocal', pid: 103 }, { name: 'gateway', pid: 100 }] })
    if (path === '/api/v1/active') return json(activeRevision)
    if (path === '/api/v1/draft') return json(revision)
    if (path === '/api/v1/logs') return json({ logs: 'smartdns ready\nhaproxy ready\ngateway ready' })
    if (path === '/api/v1/logs/stream') return json({ error: 'stream should not be used' }, 500)
    if (path === '/api/v1/update') return json({ current: '0.2.0', latest: '0.2.0', available: false })
    return json({ error: `unhandled test route ${path}` }, 500)
  })
}

for (const viewport of [{ name: 'desktop', width: 1440, height: 900 }, { name: 'mobile', width: 390, height: 844 }]) {
  test(`${viewport.name} panel pages fit the viewport`, async ({ page }) => {
    const consoleErrors: string[] = []
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
    await page.setViewportSize(viewport)
    await mockAPI(page)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-theme', /neutral/)
    await expect(page.getByText('配置状态')).toBeHidden()
    await expect(page.getByText('有草稿')).toBeHidden()
    await expect(page.getByRole('button', { name: /历史/ })).toBeHidden()

		for (const [desktopNav, mobileNav, heading] of pages) {
			const nav = viewport.name === 'desktop' ? desktopNav : mobileNav
      const button = page.getByRole('button', { name: nav, exact: true }).filter({ visible: true })
			if (nav !== '概览') await button.click()
			await expect(page.getByRole('heading', { name: heading, exact: true }).first()).toBeVisible()
			if (heading === 'DNS 与网络') {
				await expect(page.getByLabel('HAProxy 最大连接数')).toBeHidden()
				await expect(page.getByLabel('缓存条目')).toBeHidden()
				await expect(page.getByLabel('后端解析器')).toBeHidden()
				await expect(page.getByLabel('公网 DoT 监听')).toBeHidden()
				await expect(page.getByLabel('公网海外上游')).toBeVisible()
			}
			if (heading === '规则') {
				await expect(page.getByText('当前已应用规则')).toBeVisible()
				await expect(page.getByText('applied-openai')).toBeVisible()
				await expect(page.getByText('DNS 解析池 · pool:cn')).toBeVisible()
				await expect(page.getByText('还有 1 项')).toBeVisible()
			}
			if (heading === '日志') {
				await expect(page.getByText('最后刷新')).toBeVisible()
				await page.getByTitle('刷新').click()
				await expect(page.getByText('smartdns ready')).toBeVisible()
			}
			const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
			expect(overflow, `${heading} has horizontal page overflow`).toBeLessThanOrEqual(1)
		}

		const dismiss = page.getByTitle('关闭')
		if (await dismiss.isVisible()) await dismiss.click()
		await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}.png`, fullPage: true })
    expect(consoleErrors).toEqual([])
  })
}

test('theme toggle switches between neutral themes', async ({ page }) => {
  await mockAPI(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  const before = await page.locator('html').getAttribute('data-theme')
  await page.getByTitle(before === 'dark-neutral' ? '切换到浅色模式' : '切换到深色模式').click()
  const after = await page.locator('html').getAttribute('data-theme')
  expect([before, after].sort()).toEqual(['dark-neutral', 'light-neutral'])
})
