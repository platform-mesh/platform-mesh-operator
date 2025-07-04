#!/bin/bash

openssl genrsa -out webhook-config/ca.key 2048

openssl req -new -x509 -days 365 -key webhook-config/ca.key \
  -subj "/C=DE/CN=authz-server" -config webhook-config/openssl.conf \
  -out webhook-config/ca.crt

openssl req -newkey rsa:2048 -nodes -keyout webhook-config/tls.key \
  -subj "/C=DE/CN=authz-server" \
  -out webhook-config/tls.csr

  # -extfile <(printf "subjectAltName=DNS:host.containers.internal") \
openssl x509 -req \
  -days 365 \
  -extfile <(printf "subjectAltName=IP:10.96.86.219") \
  -in webhook-config/tls.csr \
  -CA webhook-config/ca.crt -CAkey webhook-config/ca.key -CAcreateserial \
  -out webhook-config/tls.crt

rm webhook-config/*.csr
