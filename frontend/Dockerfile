FROM node:22-alpine AS builder

WORKDIR /app

ARG VITE_DEFAULT_API_BASE_URL=http://api.localhost
ARG VITE_DEFAULT_BEARER_TOKEN=dev-token
ARG VITE_SITE_DOMAIN_SUFFIX=localhost

ENV VITE_DEFAULT_API_BASE_URL=$VITE_DEFAULT_API_BASE_URL
ENV VITE_DEFAULT_BEARER_TOKEN=$VITE_DEFAULT_BEARER_TOKEN
ENV VITE_SITE_DOMAIN_SUFFIX=$VITE_SITE_DOMAIN_SUFFIX

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
