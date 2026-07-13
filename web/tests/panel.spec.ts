import { expect, test, type Page, type Route } from '@playwright/test'

const managedRules = {
  imports: [
    { name: 'speedtest', type: 'sing-box', path: '', url: 'https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-speedtest.json', format: '', exit: 'direct', dns_pool: '' },
    { name: 'cn', type: 'sing-box', path: '', url: 'https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json', format: '', exit: '', dns_pool: 'cn' },
    { name: 'gfw', type: 'sing-box', path: '', url: 'https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json', format: '', exit: 'direct', dns_pool: '' },
  ],
  rules: [{ name: 'ip-check', exit: 'direct', dns_pool: '', domain_suffix: ['icanhazip.com', 'ipinfo.io', 'ippure.com'] }],
}

const bundle = {
  config: {
    system: { config_dir: '/etc/5gws', state_dir: '/var/lib/5gws', run_dir: '/run/5gws', user: 'root' },
    panel: { listen: '127.0.0.1:19443', allowed_cidrs: ['127.0.0.0/8', '::1/128'] },
    network: { gateway_ip: '10.0.0.1', internal_cidr: '10.0.0.0/24', ingress_iface: 'wwan0', http_redirect_port: 18080, https_redirect_port: 18443, quic_redirect_port: 18443, tcp_redirect_port: 18082, haproxy_max_connections: 16384, quic_policy: 'reject', encrypted_dns_policy: 'reject' },
    routing: { fallback_exit: 'direct' },
    dns: { binary: '/usr/local/bin/smartdns', dot_domain: 'dns.gateway.example.net', listen_udp: '0.0.0.0:1053', listen_tcp: '0.0.0.0:1053', listen_dot: '0.0.0.0:1853', listen_public_dot: '0.0.0.0:853', backend_resolvers: ['1.1.1.1:53', '8.8.8.8:53'], cert_dir: '/var/lib/5gws/ios', cert_file: '/etc/5gws/fullchain.pem', key_file: '/etc/5gws/privkey.pem', cache_size: 32768, upstreams_cn: ['223.5.5.5', '119.29.29.29'], upstreams_overseas_private: ['1.1.1.1'], upstreams_overseas_public: ['8.8.8.8'] },
    logging: { level: 'info', access: true },
    ios: { enabled: true, listen: '0.0.0.0:8088', base_url: 'https://dns.gateway.example.net', organization: '5gws gateway operations', profile_identifier: 'dev.5gws.dot' },
    exits: [
      { name: 'direct', type: 'direct', fwmark: 0, server: '', server_port: 0, method: '', password: '', username: '', listen_address: '', listen_port: 0, tcp: true, udp: true, timeout_seconds: 300 },
      { name: 'tokyo-shadowsocks-production-long-name', type: 'shadowsocks-rust', fwmark: 0, server: 'edge.gateway.example.net', server_port: 8388, method: '2022-blake3-aes-128-gcm', password: 'secret', username: 'default', listen_address: '127.0.0.1', listen_port: 1080, tcp: true, udp: true, timeout_seconds: 300 },
    ],
  },
  rules: {
    imports: [...managedRules.imports, { name: 'category-speedtest-global', type: 'sing-box', path: '', url: 'https://raw.githubusercontent.com/example/very/long/path/category-speedtest.json', format: '', exit: 'direct', dns_pool: '' }],
    rules: [...managedRules.rules, { name: 'openai-and-related-services', exit: 'tokyo-shadowsocks-production-long-name', dns_pool: '', domain_suffix: ['openai.com', 'chatgpt.com', 'very-long-service-domain.example.net'] }],
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

async function mockAPI(page: Page, applyHandler?: (route: Route) => Promise<unknown>) {
  await page.route('**/api/v1/**', async route => {
		const path = new URL(route.request().url()).pathname
		const json = (value: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
		if (path.startsWith('/api/v1/config/apply') && applyHandler) return applyHandler(route)
    if (path === '/api/v1/bootstrap') return json({ needs_setup: false })
    if (path === '/api/v1/me') return json({ username: 'admin' })
    if (path === '/api/v1/dashboard') return json({ version: '0.2.0', active_revision: 7, rules: 10023, processes: [{ name: 'smartdns', pid: 101 }, { name: 'haproxy', pid: 102 }, { name: 'sslocal', pid: 103 }, { name: 'gateway', pid: 100 }] })
    if (path === '/api/v1/metrics') return json({ metrics: [
      { timestamp: 1720699200, process_count: 4, rss_bytes: 52428800, tcp_connections: 31, rx_bytes: 1000000, tx_bytes: 2000000, interface: 'wwan0', dns_ok: true, dns_latency_ms: 3.2 },
      { timestamp: 1720699210, process_count: 4, rss_bytes: 53428800, tcp_connections: 35, rx_bytes: 1100000, tx_bytes: 2200000, interface: 'wwan0', dns_ok: true, dns_latency_ms: 4.1 },
    ] })
    if (path === '/api/v1/diagnostics/run') return json({ checked_at: '2026-07-12T12:00:00Z', dns: [
      { pool: 'cn', upstream: '223.5.5.5', protocol: 'udp', status: 'ok', latency_ms: 12.5, answers: ['1.2.3.4'] },
      { pool: 'overseas_private', upstream: '1.1.1.1', protocol: 'udp', status: 'ok', latency_ms: 8.2, answers: ['104.16.1.1'] },
      { pool: 'overseas_public', upstream: '8.8.8.8', protocol: 'udp', status: 'error', latency_ms: 2000, error: 'timeout' },
    ], exits: [
      { name: 'direct', type: 'direct', status: 'ok', egress_status: 'ok', egress_ip: '203.0.113.10', egress_latency_ms: 35.2 },
      { name: 'tokyo-shadowsocks-production-long-name', type: 'shadowsocks-rust', status: 'ok', upstream: 'edge.gateway.example.net:8388', upstream_status: 'ok', upstream_latency_ms: 21.1, egress_status: 'ok', egress_ip: '198.51.100.20', egress_latency_ms: 88.4 },
    ], dot: { domain: 'dns.gateway.example.net', listen: '0.0.0.0:853', status: 'ok', latency_ms: 11.2, certificate_status: 'ok', expires_at: '2026-09-10T00:00:00Z', days_remaining: 60, domain_match: true } })
    if (path === '/api/v1/config') return json(bundle)
		if (path === '/api/v1/rules/defaults') return json(managedRules)
		if (path === '/api/v1/config/validate') return json({ rule_count: 10023, warnings: [] })
		if (path === '/api/v1/config/apply') return json({ id: route.request().headers()['x-5gws-operation-id'], status: 'queued', changed: false, revision_id: 0, rule_count: 0, warnings: null, queued_at: new Date().toISOString() }, 202)
		if (path.startsWith('/api/v1/config/apply/')) return json({ id: path.split('/').pop(), status: 'succeeded', changed: false, revision_id: 7, rule_count: 10023, warnings: null, queued_at: new Date().toISOString(), finished_at: new Date().toISOString() })
    if (path === '/api/v1/active/rules') return json({
      revision_id: 7,
      active_at: activeRevision.active_at,
      rule_count: 2,
      matcher_count: 9,
      groups: [
        { key: '出口规则:exit:tokyo', title: '出口规则 · exit:tokyo-shadowsocks-production-long-name', rule_count: 1, matcher_count: 2, rules: [{ name: 'applied-openai', target: 'exit:tokyo-shadowsocks-production-long-name', matchers: [{ label: 'domain_suffix', count: 2, samples: ['openai.com', 'chatgpt.com'] }] }] },
        { key: 'DNS:pool:cn', title: 'DNS 解析池 · pool:cn', rule_count: 1, matcher_count: 7, rules: [{ name: 'applied-cn', target: 'pool:cn', matchers: [{ label: 'domain_suffix', count: 7, samples: ['example.cn', 'example.com.cn', 'service.cn', 'cdn.cn', 'portal.cn', 'static.cn'] }] }] },
      ],
    })
    if (path === '/api/v1/logs') return json({ logs: 'smartdns ready\nhaproxy ready\ngateway ready' })
    if (path === '/api/v1/logs/stream') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'id: 1\ndata: {"logs":"smartdns ready\\nhaproxy ready\\ngateway ready"}\n\n' })
    if (path === '/api/v1/ios/profile') return json({ enabled: true, profile_url: 'https://dns.gateway.example.net/ios/5gws-dot.mobileconfig', profile_qr: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=' })
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
		await expect(page.getByRole('heading', { name: '运行健康' })).toBeVisible()
		await expect(page.getByRole('button', { name: '保存' })).toBeHidden()
		await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}-overview.png`, fullPage: true })
		await expect(page.locator('header')).not.toContainText('active')
		await expect(page.locator('header')).not.toContainText('draft')
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
				await expect(page.getByRole('button', { name: '保存' })).toBeHidden()
				await expect(page.getByRole('button', { name: '预检' })).toBeVisible()
				await expect(page.getByLabel('HAProxy 最大连接数')).toBeHidden()
				await expect(page.getByLabel('缓存条目')).toBeHidden()
				await expect(page.getByLabel('后端解析器')).toBeHidden()
				await expect(page.getByLabel('公网 DoT 监听')).toBeHidden()
				await expect(page.getByLabel('公网海外上游')).toBeVisible()
				await expect(page.getByRole('heading', { name: '已应用 DNS 状态' })).toBeVisible()
				if (viewport.name === 'mobile') await page.getByRole('heading', { name: '已应用 DNS 状态' }).scrollIntoViewIfNeeded()
				await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}-dns.png`, fullPage: true })
			}
			if (heading === '出口') await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}-exits.png`, fullPage: true })
			if (heading === '规则') {
				await expect(page.getByText('当前已应用规则')).toBeVisible()
				await expect(page.getByText('applied-openai')).toBeVisible()
				await expect(page.getByText('DNS 解析池 · pool:cn')).toBeVisible()
				await expect(page.getByText('还有 1 项')).toBeVisible()
				const defaults = page.locator('section').filter({ has: page.getByRole('heading', { name: '默认规则', exact: true }) })
				for (const name of ['ip-check', 'speedtest', 'cn', 'gfw']) await expect(defaults.getByText(name, { exact: true })).toBeVisible()
				await expect(defaults.locator('input, select')).toHaveCount(0)
				await expect(defaults.getByTitle('删除')).toHaveCount(0)
				const custom = page.locator('section').filter({ has: page.getByRole('heading', { name: '自定义本地规则' }) })
				await expect(custom).toBeVisible()
				await expect(custom.getByPlaceholder('名称')).toHaveValue('openai-and-related-services')
				await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}-rules.png`, fullPage: true })
			}
			if (heading === '日志') {
				await expect(page.getByRole('button', { name: '保存' })).toBeHidden()
				await expect(page.getByPlaceholder('搜索日志')).toBeVisible()
				await expect(page.getByText('跟随')).toBeVisible()
				await page.getByTitle('刷新').click()
				await expect(page.getByText('smartdns ready')).toBeVisible()
				await page.getByPlaceholder('搜索日志').fill('haproxy')
				await expect(page.locator('pre')).toContainText('haproxy ready')
				await expect(page.locator('pre')).not.toContainText('smartdns ready')
				await page.getByPlaceholder('搜索日志').fill('')
				const download = page.waitForEvent('download')
				await page.getByTitle('下载日志').click()
				expect((await download).suggestedFilename()).toMatch(/^5gws-.*\.log$/)
				await page.screenshot({ path: `/tmp/5gws-panel-${viewport.name}-logs.png`, fullPage: true })
			}
			if (heading === '设置') {
				await expect(page.getByRole('heading', { name: '面板', exact: true })).toBeHidden()
				await expect(page.getByLabel('监听地址')).toBeHidden()
				await expect(page.getByText('允许的管理 CIDR')).toBeHidden()
				await expect(page.getByAltText('iOS Profile 二维码')).toBeVisible()
				await expect(page.getByRole('link', { name: '下载 Profile' })).toHaveAttribute('href', 'https://dns.gateway.example.net/ios/5gws-dot.mobileconfig')
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

test('preflight and apply submit the visible configuration', async ({ page }) => {
	let validateBody: typeof bundle | undefined
	let applyBody: typeof bundle | undefined
	page.on('request', request => {
		const path = new URL(request.url()).pathname
		if (path === '/api/v1/config/validate') validateBody = request.postDataJSON() as typeof bundle
		if (path === '/api/v1/config/apply') applyBody = request.postDataJSON() as typeof bundle
	})
	await mockAPI(page)
	await page.goto('/')
	await page.getByRole('button', { name: 'DNS 与网络', exact: true }).filter({ visible: true }).click()
	await page.getByLabel('Gateway IP').fill('10.0.0.2')
	await page.getByRole('button', { name: '预检' }).click()
	await expect(page.getByText('预检通过，共 10023 条规则')).toBeVisible()
	expect(validateBody?.config.network.gateway_ip).toBe('10.0.0.2')
	await page.getByRole('button', { name: '应用' }).click()
	await expect(page.getByText('配置没有变化')).toBeVisible()
	expect(applyBody?.config.network.gateway_ip).toBe('10.0.0.2')
})

test('manual rule apply reconnects with the same operation ID', async ({ page }) => {
	const operationIDs: string[] = []
	let applyBody: typeof bundle | undefined
	let posts = 0
	let polls = 0
	await mockAPI(page, async route => {
		const request = route.request()
		const path = new URL(request.url()).pathname
		const json = (value: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
		if (path === '/api/v1/config/apply' && request.method() === 'POST') {
			posts++
			operationIDs.push(request.headers()['x-5gws-operation-id'])
			applyBody = request.postDataJSON() as typeof bundle
			if (posts === 1) return route.abort('connectionfailed')
			return json({ id: operationIDs[0], status: 'running', changed: false, revision_id: 0, rule_count: 0, warnings: null, queued_at: new Date().toISOString() }, 202)
		}
		polls++
		return json({ id: operationIDs[0], status: polls === 1 ? 'running' : 'succeeded', changed: polls > 1, revision_id: polls > 1 ? 8 : 0, rule_count: polls > 1 ? 10024 : 0, warnings: null, queued_at: new Date().toISOString(), finished_at: polls > 1 ? new Date().toISOString() : undefined })
	})
	await page.goto('/')
	await page.getByRole('button', { name: '规则', exact: true }).filter({ visible: true }).first().click()
	await page.getByRole('button', { name: '新建规则', exact: true }).click()
	const localRules = page.locator('section').filter({ has: page.getByRole('heading', { name: '自定义本地规则' }) })
	await localRules.getByPlaceholder('名称').last().fill('manual-smoke')
	await localRules.getByPlaceholder('example.com, example.net').last().fill('manual-smoke.invalid')
	await page.getByRole('button', { name: '应用', exact: true }).click()
	await expect(page.getByText('数据面正在切换，正在重新连接')).toBeVisible()
	await expect(page.getByText('配置已应用，共 10024 条规则')).toBeVisible({ timeout: 7000 })
	expect(posts).toBe(2)
	expect(operationIDs[0]).toMatch(/^[0-9a-f-]{36}$/)
	expect(operationIDs[1]).toBe(operationIDs[0])
	expect(applyBody?.rules.rules).toContainEqual(expect.objectContaining({ name: 'manual-smoke', domain_suffix: ['manual-smoke.invalid'] }))
	for (const name of ['ip-check', 'openai-and-related-services', 'manual-smoke']) expect(applyBody?.rules.rules).toContainEqual(expect.objectContaining({ name }))
	for (const name of ['speedtest', 'cn', 'gfw', 'category-speedtest-global']) expect(applyBody?.rules.imports).toContainEqual(expect.objectContaining({ name }))
})
