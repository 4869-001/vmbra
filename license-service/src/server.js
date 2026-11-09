'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const { Store } = require('./store');
const { matchAny, clientIp, normalizeIp } = require('./ip');
const {
  hashPassword,
  verifyPassword,
  signToken,
  verifyToken,
  generateLicenseKey,
  normalizeLicenseKey,
  fingerprintHash,
} = require('./auth');

/* ------------------------------------------------------------------ */
/* 配置                                                                */
/* ------------------------------------------------------------------ */

const PORT = Number(process.env.PORT || 8787);
const HOST = process.env.HOST || '0.0.0.0';
const DATA_DIR = process.env.DATA_DIR || path.join(__dirname, '..', 'data');
const TRUST_PROXY = process.env.TRUST_PROXY === 'true';
const SECRET = process.env.HMAC_SECRET || 'change-me-in-production';
const ADMIN_USER = process.env.ADMIN_USER || 'admin';
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD || 'admin123';
const PUBLIC_DIR = path.join(__dirname, '..', 'public');

if (SECRET === 'change-me-in-production') {
  console.warn('[warn] HMAC_SECRET 使用默认值，生产环境必须替换：export HMAC_SECRET=$(openssl rand -hex 32)');
}

const store = new Store(DATA_DIR);
store.load();

/* 首次启动引导管理员账号 */
(function bootstrapAdmin() {
  const data = store.data;
  if (data.admins.length > 0) return;
  const { salt, hash } = hashPassword(ADMIN_PASSWORD);
  store.mutate((d) => {
    d.admins.push({
      id: store.id('adm'),
      username: ADMIN_USER,
      salt,
      hash,
      createdAt: new Date().toISOString(),
    });
  });
  console.log(`[init] 已创建管理员 ${ADMIN_USER}，请立即登录修改密码（默认密码来自 ADMIN_PASSWORD 环境变量）`);
})();

/* ------------------------------------------------------------------ */
/* 工具                                                                */
/* ------------------------------------------------------------------ */

const now = () => new Date().toISOString();

function json(res, status, body) {
  const payload = Buffer.from(JSON.stringify(body), 'utf8');
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': payload.length,
    'Cache-Control': 'no-store',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    'Access-Control-Allow-Methods': 'GET, POST, PATCH, DELETE, OPTIONS',
  });
  res.end(payload);
}

function fail(res, status, code, message, extra) {
  json(res, status, { ok: false, error: { code, message, ...(extra || {}) } });
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on('data', (c) => {
      size += c.length;
      if (size > 1024 * 1024) {
        reject(new Error('请求体过大'));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => {
      if (chunks.length === 0) return resolve({});
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
      } catch {
        reject(new Error('JSON 解析失败'));
      }
    });
    req.on('error', reject);
  });
}

function bearer(req) {
  const h = req.headers.authorization || '';
  return h.startsWith('Bearer ') ? h.slice(7).trim() : '';
}

/* ------------------------------------------------------------------ */
/* 授权判定                                                            */
/* ------------------------------------------------------------------ */

function ipAllowed(ip, data, license) {
  // 个人客户走「授权码 + 设备指纹」，家庭宽带 IP 会变，因此允许按授权码免除 IP 限制
  if (license && license.ipExempt === true) return true;

  const global = (data.ipWhitelist || []).filter((r) => r.enabled !== false);
  const perLicense = ((license && license.boundIps) || []).slice();

  if (global.length === 0 && perLicense.length === 0) {
    return data.settings.allowWhenWhitelistEmpty === true;
  }
  if (matchAny(ip, global)) return true;
  return matchAny(ip, perLicense);
}

function checkLicense(license) {
  if (!license) return { ok: false, code: 'LICENSE_NOT_FOUND', message: '授权码不存在' };
  if (license.status === 'revoked') {
    return { ok: false, code: 'LICENSE_REVOKED', message: '授权码已被停用，请联系服务商' };
  }
  if (license.status === 'suspended') {
    return { ok: false, code: 'LICENSE_SUSPENDED', message: '授权已暂停，请联系服务商' };
  }
  if (license.expiresAt && Date.now() > Date.parse(license.expiresAt)) {
    return { ok: false, code: 'LICENSE_EXPIRED', message: '授权已到期' };
  }
  return { ok: true };
}

function publicLicenseView(license) {
  return {
    id: license.id,
    plan: license.plan,
    status: license.status,
    note: license.note,
    boundIps: license.boundIps,
    ipExempt: license.ipExempt === true,
    boundFingerprint: license.boundFingerprint ? `${license.boundFingerprint.slice(0, 12)}…` : null,
    activatedAt: license.activatedAt,
    lastSeenAt: license.lastSeenAt,
    lastIp: license.lastIp,
    createdAt: license.createdAt,
    expiresAt: license.expiresAt,
  };
}

/* ------------------------------------------------------------------ */
/* 客户端接口                                                          */
/* ------------------------------------------------------------------ */

async function handleActivate(req, res) {
  const body = await readBody(req);
  const ip = clientIp(req, TRUST_PROXY);
  const data = store.data;

  const keyNorm = normalizeLicenseKey(body.licenseKey);
  if (!keyNorm) return fail(res, 400, 'BAD_REQUEST', '缺少授权码');

  const license = data.licenses.find((l) => normalizeLicenseKey(l.key) === keyNorm);

  const licenseCheck = checkLicense(license);
  if (!licenseCheck.ok) {
    store.audit('activate.denied', `激活失败：${licenseCheck.message}`, { ip, key: body.licenseKey });
    return fail(res, 403, licenseCheck.code, licenseCheck.message);
  }

  if (!ipAllowed(ip, data, license)) {
    store.audit('activate.ip_blocked', `IP 未授权：${ip}`, { ip });
    return fail(res, 403, 'IP_NOT_ALLOWED', `当前网络（${ip}）不在授权白名单内，请联系服务商开通`, {
      ip,
    });
  }

  const fp = String(body.fingerprint || '').trim();
  if (!fp) return fail(res, 400, 'BAD_REQUEST', '缺少设备指纹');

  const fpHashed = fingerprintHash(fp);
  if (license.boundFingerprint && license.boundFingerprint !== fpHashed) {
    store.audit('activate.fingerprint_mismatch', `设备指纹不匹配：${ip}`, { ip, licenseId: license.id });
    return fail(res, 403, 'FINGERPRINT_MISMATCH', '该授权码已绑定其他设备，请联系服务商解绑', {
      boundAt: license.activatedAt,
    });
  }

  if (license.status === 'unused') {
    store.mutate((d) => {
      const target = d.licenses.find((l) => l.id === license.id);
      target.status = 'active';
      target.boundFingerprint = fpHashed;
      target.activatedAt = now();
    });
  }

  store.mutate((d) => {
    const target = d.licenses.find((l) => l.id === license.id);
    target.lastSeenAt = now();
    target.lastIp = ip;
    target.activations = (target.activations || 0) + 1;
    if (target.activatedAt == null) target.activatedAt = now();
  });

  store.audit('activate.ok', `激活成功：${ip}`, {
    ip,
    licenseId: license.id,
    hostname: body.hostname,
    os: body.os,
    appVersion: body.appVersion,
  });

  const token = signToken({ sub: license.id, fp: fpHashed, typ: 'client' }, SECRET, data.settings.tokenTtlMs);

  return json(res, 200, {
    ok: true,
    token,
    tokenExpiresAt: new Date(Date.now() + data.settings.tokenTtlMs).toISOString(),
    offlineGraceMs: data.settings.offlineGraceMs,
    license: {
      id: license.id,
      plan: license.plan,
      expiresAt: license.expiresAt,
    },
    vpsEndpoints: (data.settings.vpsEndpoints || []).filter((v) => v.enabled !== false),
    client: data.settings.client,
  });
}

async function handleHeartbeat(req, res) {
  const payload = verifyToken(bearer(req), SECRET);
  if (!payload || payload.typ !== 'client') return fail(res, 401, 'INVALID_TOKEN', 'token 无效或已过期');

  const data = store.data;
  const license = data.licenses.find((l) => l.id === payload.sub);
  const licenseCheck = checkLicense(license);
  if (!licenseCheck.ok) return fail(res, 403, licenseCheck.code, licenseCheck.message);

  const ip = clientIp(req, TRUST_PROXY);
  if (!ipAllowed(ip, data, license)) {
    return fail(res, 403, 'IP_NOT_ALLOWED', `当前网络（${ip}）不在授权白名单内`, { ip });
  }

  store.mutate((d) => {
    const target = d.licenses.find((l) => l.id === license.id);
    target.lastSeenAt = now();
    target.lastIp = ip;
  });

  return json(res, 200, {
    ok: true,
    token: signToken({ sub: license.id, fp: payload.fp, typ: 'client' }, SECRET, data.settings.tokenTtlMs),
    tokenExpiresAt: new Date(Date.now() + data.settings.tokenTtlMs).toISOString(),
    vpsEndpoints: (data.settings.vpsEndpoints || []).filter((v) => v.enabled !== false),
    client: data.settings.client,
  });
}

/** 客户端一键配置所需的步骤清单 */
function handleSetupManifest(req, res) {
  const payload = verifyToken(bearer(req), SECRET);
  if (!payload || payload.typ !== 'client') return fail(res, 401, 'INVALID_TOKEN', 'token 无效或已过期');
  const data = store.data;
  return json(res, 200, {
    ok: true,
    steps: [
      { id: 'clt', title: '检查 Xcode 命令行工具', optional: false },
      { id: 'brew', title: '准备 Homebrew 包管理器', optional: false },
      { id: 'git', title: '安装 Git', optional: false },
      { id: 'node', title: '安装 Node.js LTS', optional: false },
      { id: 'vps', title: '校验 VPS 出口连通性', optional: false },
      { id: 'vm', title: '准备 lima 隔离虚拟机', optional: false },
      { id: 'guest', title: '虚拟机内安装 git / node / npm', optional: true },
      { id: 'claude', title: '安装 Claude Code', optional: false },
      { id: 'verify', title: '环境自检', optional: false },
    ],
    client: data.settings.client,
  });
}

/* ------------------------------------------------------------------ */
/* 管理端接口                                                          */
/* ------------------------------------------------------------------ */

function requireAdmin(req, res) {
  const payload = verifyToken(bearer(req), SECRET);
  if (!payload || payload.typ !== 'admin') {
    fail(res, 401, 'UNAUTHORIZED', '未登录或登录已过期');
    return null;
  }
  return payload;
}

async function handleLogin(req, res) {
  const body = await readBody(req);
  const data = store.data;
  const admin = data.admins.find((a) => a.username === String(body.username || '').trim());
  if (!admin || !verifyPassword(body.password || '', admin.salt, admin.hash)) {
    store.audit('admin.login_failed', `登录失败：${body.username}`, { ip: clientIp(req, TRUST_PROXY) });
    return fail(res, 401, 'BAD_CREDENTIALS', '用户名或密码错误');
  }
  store.audit('admin.login', `管理员登录：${admin.username}`, { ip: clientIp(req, TRUST_PROXY) });
  return json(res, 200, {
    ok: true,
    token: signToken({ sub: admin.id, username: admin.username, typ: 'admin' }, SECRET, 12 * 60 * 60 * 1000),
    username: admin.username,
    serverIp: clientIp(req, TRUST_PROXY),
  });
}

async function handleChangePassword(req, res) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  if (!body.newPassword || String(body.newPassword).length < 8) {
    return fail(res, 400, 'WEAK_PASSWORD', '新密码至少 8 位');
  }
  const admin = store.data.admins.find((a) => a.id === payload.sub);
  if (!admin || !verifyPassword(body.oldPassword || '', admin.salt, admin.hash)) {
    return fail(res, 401, 'BAD_CREDENTIALS', '原密码错误');
  }
  const { salt, hash } = hashPassword(body.newPassword);
  store.mutate((d) => {
    const target = d.admins.find((a) => a.id === payload.sub);
    target.salt = salt;
    target.hash = hash;
  });
  return json(res, 200, { ok: true });
}

function handleOverview(req, res) {
  if (!requireAdmin(req, res)) return;
  const data = store.data;
  const licenses = data.licenses;
  return json(res, 200, {
    ok: true,
    stats: {
      total: licenses.length,
      active: licenses.filter((l) => l.status === 'active').length,
      unused: licenses.filter((l) => l.status === 'unused').length,
      revoked: licenses.filter((l) => l.status === 'revoked').length,
      ips: (data.ipWhitelist || []).filter((r) => r.enabled !== false).length,
      vps: (data.settings.vpsEndpoints || []).filter((v) => v.enabled !== false).length,
      online24h: licenses.filter((l) => l.lastSeenAt && Date.now() - Date.parse(l.lastSeenAt) < 864e5).length,
    },
    settings: data.settings,
  });
}

async function handleListLicenses(req, res) {
  if (!requireAdmin(req, res)) return;
  return json(res, 200, {
    ok: true,
    items: store.data.licenses.map((l) => ({ ...publicLicenseView(l), key: l.key })),
  });
}

async function handleCreateLicenses(req, res) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  const count = Math.min(Math.max(Number(body.count) || 1, 1), 200);
  const plan = body.plan || 'lifetime';
  const note = String(body.note || '').slice(0, 200);
  const expiresAt = plan === 'lifetime' ? null : body.expiresAt || null;

  const created = [];
  store.mutate((d) => {
    for (let i = 0; i < count; i += 1) {
      const item = {
        id: store.id('lic'),
        key: generateLicenseKey(),
        plan,
        status: 'unused',
        note,
        ipExempt: body.ipExempt === true,
        boundIps: [],
        boundFingerprint: null,
        activations: 0,
        activatedAt: null,
        lastSeenAt: null,
        lastIp: null,
        createdAt: now(),
        expiresAt,
        createdBy: payload.username,
      };
      d.licenses.unshift(item);
      created.push(item);
    }
  });
  store.audit('license.create', `生成 ${count} 个授权码`, { by: payload.username, plan });
  return json(res, 201, { ok: true, items: created.map((c) => ({ id: c.id, key: c.key })) });
}

async function handleUpdateLicense(req, res, id) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  const result = store.mutate((d) => {
    const target = d.licenses.find((l) => l.id === id);
    if (!target) return null;
    if (body.status && ['active', 'unused', 'revoked', 'suspended'].includes(body.status)) {
      target.status = body.status;
    }
    if (typeof body.note === 'string') target.note = body.note.slice(0, 200);
    if (body.ipExempt !== undefined) target.ipExempt = body.ipExempt === true;
    if (Array.isArray(body.boundIps)) target.boundIps = body.boundIps.map(String).filter(Boolean);
    if (body.expiresAt !== undefined) target.expiresAt = body.expiresAt || null;
    if (body.resetBinding === true) {
      target.boundFingerprint = null;
      target.status = 'unused';
      target.activatedAt = null;
    }
    return target;
  });
  if (!result) return fail(res, 404, 'NOT_FOUND', '授权码不存在');
  store.audit('license.update', `更新授权码 ${result.key}`, { by: payload.username, changes: Object.keys(body) });
  return json(res, 200, { ok: true, item: { ...publicLicenseView(result), key: result.key } });
}

function handleDeleteLicense(req, res, id) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const removed = store.mutate((d) => {
    const idx = d.licenses.findIndex((l) => l.id === id);
    if (idx === -1) return null;
    return d.licenses.splice(idx, 1)[0];
  });
  if (!removed) return fail(res, 404, 'NOT_FOUND', '授权码不存在');
  store.audit('license.delete', `删除授权码 ${removed.key}`, { by: payload.username });
  return json(res, 200, { ok: true });
}

async function handleCreateIp(req, res) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  const value = normalizeIp(body.value || body.ip || '');
  if (!value) return fail(res, 400, 'BAD_REQUEST', 'IP 不能为空');

  const item = store.mutate((d) => {
    const entry = {
      id: store.id('ip'),
      value,
      note: String(body.note || '').slice(0, 200),
      enabled: body.enabled !== false,
      createdAt: now(),
      createdBy: payload.username,
    };
    d.ipWhitelist.unshift(entry);
    return entry;
  });
  store.audit('ip.add', `加入白名单 ${value}`, { by: payload.username });
  return json(res, 201, { ok: true, item });
}

async function handleUpdateIp(req, res, id) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  const item = store.mutate((d) => {
    const target = d.ipWhitelist.find((r) => r.id === id);
    if (!target) return null;
    if (body.value !== undefined) target.value = normalizeIp(body.value);
    if (body.note !== undefined) target.note = String(body.note).slice(0, 200);
    if (body.enabled !== undefined) target.enabled = Boolean(body.enabled);
    return target;
  });
  if (!item) return fail(res, 404, 'NOT_FOUND', '记录不存在');
  return json(res, 200, { ok: true, item });
}

function handleDeleteIp(req, res, id) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const removed = store.mutate((d) => {
    const idx = d.ipWhitelist.findIndex((r) => r.id === id);
    if (idx === -1) return null;
    return d.ipWhitelist.splice(idx, 1)[0];
  });
  if (!removed) return fail(res, 404, 'NOT_FOUND', '记录不存在');
  store.audit('ip.remove', `移出白名单 ${removed.value}`, { by: payload.username });
  return json(res, 200, { ok: true });
}

async function handleCreateVps(req, res) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const body = await readBody(req);
  const host = String(body.host || '').trim();
  if (!host) return fail(res, 400, 'BAD_REQUEST', 'host 不能为空');
  const item = store.mutate((d) => {
    const entry = {
      id: store.id('vps'),
      name: String(body.name || host).slice(0, 60),
      host,
      port: Number(body.port) || 443,
      region: String(body.region || '').slice(0, 40),
      enabled: body.enabled !== false,
    };
    d.settings.vpsEndpoints.unshift(entry);
    return entry;
  });
  store.audit('vps.add', `新增出口节点 ${host}`, { by: payload.username });
  return json(res, 201, { ok: true, item });
}

function handleDeleteVps(req, res, id) {
  const payload = requireAdmin(req, res);
  if (!payload) return;
  const removed = store.mutate((d) => {
    const idx = d.settings.vpsEndpoints.findIndex((v) => v.id === id);
    if (idx === -1) return null;
    return d.settings.vpsEndpoints.splice(idx, 1)[0];
  });
  if (!removed) return fail(res, 404, 'NOT_FOUND', '节点不存在');
  return json(res, 200, { ok: true });
}

function handleEvents(req, res) {
  if (!requireAdmin(req, res)) return;
  return json(res, 200, { ok: true, items: store.data.events.slice(0, 300) });
}

/* ------------------------------------------------------------------ */
/* 路由                                                                */
/* ------------------------------------------------------------------ */

function serveStatic(req, res, file) {
  const full = path.join(PUBLIC_DIR, file);
  if (!full.startsWith(PUBLIC_DIR) || !fs.existsSync(full)) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('Not Found');
  }
  const ext = path.extname(full);
  const type =
    ext === '.html'
      ? 'text/html; charset=utf-8'
      : ext === '.js'
        ? 'text/javascript; charset=utf-8'
        : ext === '.css'
          ? 'text/css; charset=utf-8'
          : 'application/octet-stream';
  res.writeHead(200, { 'Content-Type': type });
  fs.createReadStream(full).pipe(res);
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  const pathname = url.pathname.replace(/\/+$/, '') || '/';
  const method = req.method;

  if (method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
      'Access-Control-Allow-Methods': 'GET, POST, PATCH, DELETE, OPTIONS',
    });
    return res.end();
  }

  try {
    /* 管理端页面 */
    if (method === 'GET' && (pathname === '/admin' || pathname === '/')) {
      return serveStatic(req, res, 'index.html');
    }

    /* 客户端 API */
    if (method === 'GET' && pathname === '/api/v1/health') {
      return json(res, 200, { ok: true, service: 'vmbra-license', time: now() });
    }
    if (method === 'POST' && pathname === '/api/v1/activate') return await handleActivate(req, res);
    if (method === 'POST' && pathname === '/api/v1/heartbeat') return await handleHeartbeat(req, res);
    if (method === 'GET' && pathname === '/api/v1/setup/manifest') return handleSetupManifest(req, res);

    /* 管理端 API */
    if (method === 'POST' && pathname === '/api/admin/login') return await handleLogin(req, res);
    if (method === 'POST' && pathname === '/api/admin/password') return await handleChangePassword(req, res);
    if (method === 'GET' && pathname === '/api/admin/overview') return handleOverview(req, res);
    if (method === 'GET' && pathname === '/api/admin/events') return handleEvents(req, res);

    if (pathname === '/api/admin/licenses') {
      if (method === 'GET') return await handleListLicenses(req, res);
      if (method === 'POST') return await handleCreateLicenses(req, res);
    }
    let m = pathname.match(/^\/api\/admin\/licenses\/([\w-]+)$/);
    if (m) {
      if (method === 'PATCH') return await handleUpdateLicense(req, res, m[1]);
      if (method === 'DELETE') return handleDeleteLicense(req, res, m[1]);
    }

    if (pathname === '/api/admin/ips') {
      if (method === 'GET') {
        if (!requireAdmin(req, res)) return;
        return json(res, 200, { ok: true, items: store.data.ipWhitelist });
      }
      if (method === 'POST') return await handleCreateIp(req, res);
    }
    m = pathname.match(/^\/api\/admin\/ips\/([\w-]+)$/);
    if (m) {
      if (method === 'PATCH') return await handleUpdateIp(req, res, m[1]);
      if (method === 'DELETE') return handleDeleteIp(req, res, m[1]);
    }

    if (pathname === '/api/admin/vps') {
      if (method === 'POST') return await handleCreateVps(req, res);
    }
    m = pathname.match(/^\/api\/admin\/vps\/([\w-]+)$/);
    if (m && method === 'DELETE') return handleDeleteVps(req, res, m[1]);

    return fail(res, 404, 'NOT_FOUND', `未知接口 ${method} ${pathname}`);
  } catch (err) {
    console.error('[error]', err);
    return fail(res, 500, 'INTERNAL', err.message || '服务内部错误');
  }
});

server.listen(PORT, HOST, () => {
  console.log(`Vmbra 授权中心已启动: http://${HOST}:${PORT}`);
  console.log(`管理端: http://127.0.0.1:${PORT}/admin`);
  console.log(`数据目录: ${DATA_DIR}`);
});
