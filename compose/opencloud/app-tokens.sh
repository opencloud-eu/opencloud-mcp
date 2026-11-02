#!/bin/sh
# an app token for alice and bob, made once and kept in /tokens, printed on every start as the
# environment of the live test: alice the user, bob the second one
set -eu

rauthy=http://id.localhost/auth/v1
cloud=http://cloud.localhost
# curl answers every *.localhost name with loopback itself, past the network alias
reach='--connect-to id.localhost:80:rauthy:9201 --connect-to cloud.localhost:80:opencloud:9200'
client='opencloud-mcp:local-compose-opencloud-mcp-client-secret-that-is-64-characters-'
token_type=urn:ietf:params:oauth:token-type:access_token

field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }

until curl $reach -fs -o /dev/null "$cloud/status.php"; do sleep 2; done

for user in alice bob; do
  if [ ! -s "/tokens/$user" ]; then
    # OpenCloud takes only tokens issued for itself, so the sign-in is exchanged first
    subject=$(curl $reach -fsS "$rauthy/oidc/token" \
      --data-urlencode grant_type=password --data-urlencode client_id=live-test \
      --data-urlencode "username=$user@example.com" --data-urlencode password=demo | field access_token)
    access=$(curl $reach -fsS -u "$client" "$rauthy/oidc/token" \
      --data-urlencode grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
      --data-urlencode "subject_token=$subject" --data-urlencode "subject_token_type=$token_type" \
      --data-urlencode "requested_token_type=$token_type" --data-urlencode "resource=$cloud" | field access_token)
    # the first request signs the user up; 876000h is a hundred years, OpenCloud has no unlimited token
    curl $reach -fsS -X POST -H "Authorization: Bearer $access" "$cloud/auth-app/tokens?expiry=876000h" \
      | field token > "/tokens/$user.new"
    [ -s "/tokens/$user.new" ] && mv "/tokens/$user.new" "/tokens/$user"
  fi
done

# a token is words and spaces
echo "export OPENCLOUD_URL=$cloud OPENCLOUD_USER=alice OPENCLOUD_APP_TOKEN='$(cat /tokens/alice)'"
echo "export OPENCLOUD_PEER_USER=bob OPENCLOUD_PEER_APP_TOKEN='$(cat /tokens/bob)'"
