#!/bin/sh
# lets the client opencloud-mcp exchange a person's token for one with OpenCloud's audience
set -eu

rauthy=http://id.localhost/auth/v1
# curl answers every *.localhost name with loopback itself, past the network alias
reach='--connect-to id.localhost:80:rauthy:9201'
key='setup$local-compose-setup-api-key-secret-that-is-at-least-64-characters'

until curl $reach -fs -o /dev/null "$rauthy/.well-known/openid-configuration"; do sleep 1; done

curl $reach -fsS -o /dev/null -X PUT "$rauthy/clients/opencloud-mcp" \
  -H "Authorization: API-Key $key" -H 'Content-Type: application/json' \
  --data @- <<'JSON'
{
  "name": "OpenCloud MCP server",
  "confidential": true,
  "redirect_uris": [],
  "enabled": true,
  "flows_enabled": ["urn:ietf:params:oauth:grant-type:token-exchange"],
  "access_token_alg": "RS256",
  "id_token_alg": "RS256",
  "auth_code_lifetime": 60,
  "access_token_lifetime": 300,
  "scopes": ["openid", "profile", "email", "groups"],
  "default_scopes": ["openid", "profile", "email", "groups"],
  "force_mfa": false,
  "claims_at_root": false,
  "allowed_resources": ["http://cloud.localhost"]
}
JSON

# the live test signs alice in with a password, a grant that takes no resource, so its tokens
# name the MCP server by default
curl $reach -fsS -o /dev/null -X PUT "$rauthy/clients/live-test" \
  -H "Authorization: API-Key $key" -H 'Content-Type: application/json' \
  --data @- <<'JSON'
{
  "name": "opencloud-mcp live test",
  "confidential": false,
  "redirect_uris": [],
  "enabled": true,
  "flows_enabled": ["password"],
  "access_token_alg": "RS256",
  "id_token_alg": "RS256",
  "auth_code_lifetime": 60,
  "access_token_lifetime": 300,
  "scopes": ["openid", "profile", "email", "groups"],
  "default_scopes": ["openid", "profile", "email", "groups"],
  "force_mfa": false,
  "claims_at_root": false,
  "default_aud": ["http://mcp.localhost/mcp"]
}
JSON
