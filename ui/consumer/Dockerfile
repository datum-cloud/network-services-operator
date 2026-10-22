FROM node:26.12.1-alpine3.24@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797 AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY . .
RUN npm run build

FROM node:26.12.1-alpine3.24@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797
RUN apk add --no-cache --upgrade 'ca-certificates>=20261020-r0'
RUN ! grep -q MIICTjCCAdOgAwIBAgIKPPYHqWhwDtqLhDAKBggqhkjOPQQDAzBrMQswCQYDVQQG /etc/ssl/certs/ca-certificates.crt
RUN node -e 'const { rootCertificates } = require("node:tls"); const { X509Certificate } = require("node:crypto"); const compromised = rootCertificates.some((pem) => new X509Certificate(pem).subject.split("\n").includes("CN=emSign ECC Root CA - G3")); if (compromised) { console.error("refusing to build: Node trusts emSign ECC Root CA - G3"); process.exit(1); }'
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY serve.ts ./
USER 1000:1000
EXPOSE 8443
CMD ["node", "serve.ts"]
