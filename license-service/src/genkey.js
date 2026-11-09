#!/usr/bin/env node
'use strict';

/**
 * 离线生成授权码（无需启动服务）
 * 用法：node src/genkey.js -n 5 --plan lifetime --note "微信昵称"
 */

const path = require('path');
const { Store } = require('./store');
const { generateLicenseKey } = require('./auth');

const args = process.argv.slice(2);
const get = (flag, fallback) => {
  const i = args.indexOf(flag);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};

const count = Math.min(Math.max(Number(get('-n', '1')) || 1, 1), 500);
const plan = get('--plan', 'lifetime');
const note = get('--note', '');

const store = new Store(process.env.DATA_DIR || path.join(__dirname, '..', 'data'));
store.load();

const created = store.mutate((data) => {
  const list = [];
  for (let i = 0; i < count; i += 1) {
    const item = {
      id: store.id('lic'),
      key: generateLicenseKey(),
      plan,
      status: 'unused',
      note,
      ipExempt: args.includes('--ip-exempt'),
      boundIps: [],
      boundFingerprint: null,
      activations: 0,
      activatedAt: null,
      lastSeenAt: null,
      lastIp: null,
      createdAt: new Date().toISOString(),
      expiresAt: null,
      createdBy: 'cli',
    };
    data.licenses.unshift(item);
    list.push(item);
  }
  return list;
});

created.forEach((item) => console.log(item.key));
console.error(`\n已生成 ${created.length} 个 ${plan} 授权码`);
