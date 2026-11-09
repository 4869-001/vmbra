'use strict';

const crypto = require('crypto');

/** ---------- 密码 ---------- */

function hashPassword(password, salt) {
  const s = salt || crypto.randomBytes(16).toString('hex');
  const hash = crypto.scryptSync(String(password), s, 64).toString('hex');
  return { salt: s, hash };
}

function verifyPassword(password, salt, hash) {
  const { hash: candidate } = hashPassword(password, salt);
  const a = Buffer.from(candidate, 'hex');
  const b = Buffer.from(String(hash), 'hex');
  if (a.length !== b.length) return false;
  return crypto.timingSafeEqual(a, b);
}

/** ---------- 签名 token（无第三方依赖） ---------- */

const b64url = (buf) =>
  Buffer.from(buf).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

const unb64url = (str) => Buffer.from(String(str).replace(/-/g, '+').replace(/_/g, '/'), 'base64');

function signToken(payload, secret, ttlMs) {
  const body = { ...payload };
  if (ttlMs) body.exp = Date.now() + ttlMs;
  const data = b64url(JSON.stringify(body));
  const sig = b64url(crypto.createHmac('sha256', secret).update(data).digest());
  return `${data}.${sig}`;
}

function verifyToken(token, secret) {
  if (!token || typeof token !== 'string') return null;
  const parts = token.split('.');
  if (parts.length !== 2) return null;
  const [data, sig] = parts;
  const expected = b64url(crypto.createHmac('sha256', secret).update(data).digest());
  const a = Buffer.from(sig);
  const b = Buffer.from(expected);
  if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) return null;
  let payload;
  try {
    payload = JSON.parse(unb64url(data).toString('utf8'));
  } catch {
    return null;
  }
  if (payload.exp && Date.now() > payload.exp) return null;
  return payload;
}

/** ---------- 授权码 ---------- */

// 去掉易混淆字符（0/O、1/I/L），便于电话/微信口述
const ALPHABET = '23456789ABCDEFGHJKMNPQRSTUVWXYZ';

function generateLicenseKey(prefix = 'VMB') {
  const bytes = crypto.randomBytes(16);
  const chars = [];
  for (let i = 0; i < 16; i += 1) chars.push(ALPHABET[bytes[i] % ALPHABET.length]);
  const groups = chars.join('').match(/.{4}/g);
  return `${prefix}-${groups.join('-')}`;
}

function normalizeLicenseKey(input) {
  return String(input || '')
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
    .replace(/^VMB/, '');
}

/** ---------- 机器指纹 ---------- */

function fingerprintHash(fingerprint) {
  return crypto.createHash('sha256').update(String(fingerprint || '')).digest('hex');
}

module.exports = {
  hashPassword,
  verifyPassword,
  signToken,
  verifyToken,
  generateLicenseKey,
  normalizeLicenseKey,
  fingerprintHash,
};
