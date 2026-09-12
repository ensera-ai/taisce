#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# Native vLLM TLS preserves the application's HTTPS requirement on Docker's private network.
# The temporary CA signs only this run; its signing key is removed immediately after issuance.
set -euo pipefail
umask 077
test ! -e qualification-tls
mkdir qualification-tls
openssl req -x509 -newkey rsa:3072 -nodes -days 1 \
    -subj '/CN=Taisce disposable qualification CA' \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -keyout qualification-tls/ca.key -out qualification-tls/ca.crt
openssl req -new -newkey rsa:3072 -nodes -subj '/CN=generation' \
    -keyout qualification-tls/server.key -out qualification-tls/server.csr
cat > qualification-tls/server.ext <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:generation,DNS:embedding,IP:127.0.0.1
EOF
openssl x509 -req -in qualification-tls/server.csr -CA qualification-tls/ca.crt \
    -CAkey qualification-tls/ca.key -CAcreateserial -days 1 \
    -extfile qualification-tls/server.ext -out qualification-tls/server.crt
rm qualification-tls/ca.key qualification-tls/server.csr qualification-tls/server.ext
# OpenSSL versions that choose a random serial may not leave a serial-state file.
rm -f qualification-tls/ca.srl
chmod 644 qualification-tls/ca.crt qualification-tls/server.crt
openssl verify -CAfile qualification-tls/ca.crt qualification-tls/server.crt
