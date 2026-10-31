FROM node:26.13.0-alpine3.24@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94 AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY . .
RUN npm run build

FROM node:26.13.0-alpine3.24@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94
RUN apk add --no-cache --upgrade 'ca-certificates>=20261020-r0'
RUN ! grep -q MIICTjCCAdOgAwIBAgIKPPYHqWhwDtqLhDAKBggqhkjOPQQDAzBrMQswCQYDVQQG /etc/ssl/certs/ca-certificates.crt
RUN node -e 'const { rootCertificates } = require("node:tls"); const { X509Certificate } = require("node:crypto"); const compromised = rootCertificates.some((pem) => new X509Certificate(pem).subject.split("\n").includes("CN=emSign ECC Root CA - G3")); if (compromised) { console.error("refusing to build: Node trusts emSign ECC Root CA - G3"); process.exit(1); }'
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY serve.ts ./
USER 1000:1000
EXPOSE 8443
CMD ["node", "serve.ts"]
