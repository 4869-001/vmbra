'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const EMPTY = {
  version: 1,
  admins: [],
  licenses: [],
  ipWhitelist: [],
  events: [],
  settings: {
    /** 白名单为空时是否放行。商业产品应保持 false（安全默认）。 */
    allowWhenWhitelistEmpty: false,
    /** 单条授权码最大可绑定 IP 数，0 表示不限 */
    maxIpsPerLicense: 3,
    /** 客户端 token 有效期（毫秒），默认 7 天 */
    tokenTtlMs: 7 * 24 * 60 * 60 * 1000,
    /** 客户端离线宽限（毫秒），服务器不可达时允许继续使用 72 小时 */
    offlineGraceMs: 72 * 60 * 60 * 1000,
    /** 客户端最新版本与下载地址 */
    client: {
      minVersion: '1.0.0',
      latestVersion: '1.0.0',
      downloadUrl: '',
      setupScriptUrl: '',
    },
    /** 授权通过后才下发的出口节点列表 */
    vpsEndpoints: [],
  },
};

class Store {
  constructor(dir) {
    this.dir = dir;
    this.file = path.join(dir, 'db.json');
    this.data = null;
  }

  load() {
    fs.mkdirSync(this.dir, { recursive: true });
    if (!fs.existsSync(this.file)) {
      this.data = JSON.parse(JSON.stringify(EMPTY));
      this.save();
      return this.data;
    }
    const raw = fs.readFileSync(this.file, 'utf8');
    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      const backup = `${this.file}.corrupt-${Date.now()}`;
      fs.copyFileSync(this.file, backup);
      throw new Error(`db.json 解析失败，已备份到 ${backup}: ${err.message}`);
    }
    this.data = {
      ...JSON.parse(JSON.stringify(EMPTY)),
      ...parsed,
      settings: { ...EMPTY.settings, ...(parsed.settings || {}) },
    };
    return this.data;
  }

  /** 原子写入，避免断电/并发导致 JSON 损坏 */
  save() {
    fs.mkdirSync(this.dir, { recursive: true });
    const tmp = `${this.file}.${process.pid}.tmp`;
    fs.writeFileSync(tmp, JSON.stringify(this.data, null, 2), 'utf8');
    fs.renameSync(tmp, this.file);
  }

  get(fn) {
    return fn(this.data);
  }

  mutate(fn) {
    const result = fn(this.data);
    this.save();
    return result;
  }

  id(prefix) {
    return `${prefix}_${crypto.randomBytes(6).toString('hex')}`;
  }

  log(type, message, meta = {}) {
    const entry = {
      id: this.id('evt'),
      type,
      message,
      meta,
      at: new Date().toISOString(),
    };
    this.data.events.unshift(entry);
    if (this.data.events.length > 2000) this.data.events.length = 2000;
    return entry;
  }

  /** 记录事件并落盘 */
  audit(type, message, meta) {
    return this.mutate((data) => {
      const entry = {
        id: this.id('evt'),
        type,
        message,
        meta: meta || {},
        at: new Date().toISOString(),
      };
      data.events.unshift(entry);
      if (data.events.length > 2000) data.events.length = 2000;
      return entry;
    });
  }
}

module.exports = { Store };
