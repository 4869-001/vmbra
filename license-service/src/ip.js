'use strict';

/**
 * IP 归一化与匹配。
 * 只对 IPv4 做 CIDR 计算，IPv6 走精确匹配（绝大多数客户场景够用）。
 */

function normalizeIp(raw) {
  if (!raw) return '';
  let ip = String(raw).trim();
  if (ip.startsWith('[') && ip.includes(']')) ip = ip.slice(1, ip.indexOf(']'));
  if (ip.startsWith('::ffff:')) ip = ip.slice(7);
  if (ip.startsWith('::FFFF:')) ip = ip.slice(7);
  if (ip === '::1') ip = '127.0.0.1';
  return ip;
}

function isIpv4(ip) {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(ip);
}

function ipv4ToInt(ip) {
  const parts = ip.split('.').map(Number);
  if (parts.length !== 4 || parts.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) {
    return null;
  }
  return ((parts[0] << 24) >>> 0) + (parts[1] << 16) + (parts[2] << 8) + parts[3];
}

/** 单条规则匹配：支持 "1.2.3.4" 与 "1.2.3.0/24" */
function matchRule(ip, rule) {
  if (!rule) return false;
  const target = normalizeIp(ip);
  const r = String(rule).trim();
  if (!r) return false;

  if (r.includes('/')) {
    if (!isIpv4(target)) return false;
    const [base, bitsRaw] = r.split('/');
    const bits = Number(bitsRaw);
    const baseInt = ipv4ToInt(normalizeIp(base));
    const ipInt = ipv4ToInt(target);
    if (baseInt === null || ipInt === null) return false;
    if (!Number.isInteger(bits) || bits < 0 || bits > 32) return false;
    if (bits === 0) return true;
    const mask = bits === 32 ? 0xffffffff : (0xffffffff << (32 - bits)) >>> 0;
    return ((baseInt & mask) >>> 0) === ((ipInt & mask) >>> 0);
  }

  return normalizeIp(r) === target;
}

/** rules: [{ value|ip|cidr, enabled }] 或字符串数组 */
function matchAny(ip, rules) {
  const list = Array.isArray(rules) ? rules : [];
  return list.some((item) => {
    if (item == null) return false;
    if (typeof item === 'string') return matchRule(ip, item);
    if (item.enabled === false) return false;
    return matchRule(ip, item.value || item.ip || item.cidr);
  });
}

/** 从请求中解析客户端真实 IP */
function clientIp(req, trustProxy) {
  if (trustProxy) {
    const xff = req.headers['x-forwarded-for'];
    if (xff) {
      const first = String(xff).split(',')[0].trim();
      if (first) return normalizeIp(first);
    }
    const real = req.headers['x-real-ip'];
    if (real) return normalizeIp(String(real));
  }
  return normalizeIp(req.socket && req.socket.remoteAddress);
}

module.exports = { normalizeIp, matchRule, matchAny, clientIp, isIpv4 };
