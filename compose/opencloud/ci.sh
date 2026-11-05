#!/bin/sh
# the OpenCloud service of CI made ready for the live tests: alice and bob with the password demo,
# the seed of the demo, and an app token of each, printed as the environment of the tests
set -eu

cloud=$1
admin=admin:$2

until curl -fs -o /dev/null "$cloud/status.php"; do sleep 2; done

# OpenCloud's own users, made by admin; a second run finds them there
for user in alice bob; do
  name=$(printf '%s' "$user" | awk '{ print toupper(substr($0, 1, 1)) substr($0, 2) }')
  until code=$(curl -s -o /dev/null -w '%{http_code}' -u "$admin" -H 'Content-Type: application/json' "$cloud/graph/v1.0/users" \
    --data "{\"onPremisesSamAccountName\":\"$user\",\"displayName\":\"$name\",\"mail\":\"$user@example.com\",\"passwordProfile\":{\"password\":\"demo\"}}") &&
    [ "$code" = 201 ] || [ "$code" = 409 ]; do
    sleep 2
  done
done

SEED_CLOUD=$cloud SEED_ADMIN_PASSWORD=$2 sh "$(dirname "$0")/seed.sh" >&2

token() {
  curl -fsS -u "$1:demo" -X POST "$cloud/auth-app/tokens?expiry=2h" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
}

alice=$(token alice)
bob=$(token bob)

if [ -z "$alice" ] || [ -z "$bob" ]; then
  echo "no app token for alice or bob" >&2
  exit 1
fi

# a token is words and spaces
echo "export OPENCLOUD_URL=$cloud OPENCLOUD_LIVE_ANY_HOST=true OPENCLOUD_USER=alice OPENCLOUD_APP_TOKEN='$alice'"
echo "export OPENCLOUD_PEER_USER=bob OPENCLOUD_PEER_APP_TOKEN='$bob'"
