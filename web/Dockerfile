FROM node:20-alpine AS builder

WORKDIR /app
COPY web/package.json ./
RUN npm install

COPY web/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80

HEALTHCHECK --interval=15s --timeout=3s --retries=5 CMD wget -qO- http://localhost || exit 1
FROM node:20-alpine AS builder

WORKDIR /app
COPY web/package.json ./
RUN npm install

COPY web/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80

HEALTHCHECK --interval=15s --timeout=3s --retries=5 CMD wget -qO- http://localhost || exit 1
