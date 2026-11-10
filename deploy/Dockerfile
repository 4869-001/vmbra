# Vmbra 授权中心镜像（Node ≥18，零第三方依赖）
# 构建上下文是仓库根目录：docker build -f deploy/Dockerfile .
FROM node:20-alpine

ENV NODE_ENV=production \
    PORT=8787 \
    HOST=0.0.0.0 \
    DATA_DIR=/data

WORKDIR /app
COPY license-service/package.json ./package.json
COPY license-service/src ./src
COPY license-service/public ./public

RUN addgroup -S vmbra \
 && adduser -S -G vmbra -h /app vmbra \
 && mkdir -p /data \
 && chown -R vmbra:vmbra /app /data

USER vmbra
EXPOSE 8787

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8787/api/v1/health >/dev/null || exit 1

CMD ["node", "src/server.js"]
