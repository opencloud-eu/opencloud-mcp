#!/bin/sh
# alice as a space admin, the group Support with bob in it, and the project space Handbook with
# alice as its manager and bob as a viewer, made once by admin beside the running server; the
# opencloud CLI only reaches the services from inside this container; CI runs it from outside with
# SEED_CLOUD and admin's password in SEED_ADMIN_PASSWORD, by basic auth
set -eu

cloud=${SEED_CLOUD:-http://localhost:9200}
manager=312c0871-5ef7-4b3a-85b6-0e4074c64049
viewer=a8d5fe5e-96e3-418d-825b-534dbdf22b99
# the role Space Admin is the same on every instance, its application is made by opencloud init
space_admin=2aadd357-682c-406b-8874-293091995fdd

# the first "name":"value" of the answer
field() { grep -o "\"$1\":\"[^\"]*\"" | head -n 1 | cut -d '"' -f 4; }

until curl -fs -o /dev/null "$cloud/status.php"; do sleep 2; done

# a token of admin for ten minutes
login() {
  if [ -n "${SEED_ADMIN_PASSWORD:-}" ]; then
    token=$SEED_ADMIN_PASSWORD
    return
  fi

  token=''
  until [ -n "$token" ]; do
    token=$(opencloud auth-app create --user-name admin --expiration 10m 2>/dev/null | sed -n 's/^ *token: //p') || true
    [ -n "$token" ] || sleep 2
  done
}

api() { curl -fsS -u "admin:$token" "$@"; }

# alice and bob exist once they signed in, which the app-tokens service does, however long it takes
id_of() {
  until id=$(api "$cloud/graph/v1.0/users?\$search=$1" 2>/dev/null | field id) && [ -n "$id" ]; do
    [ "$(curl -s -o /dev/null -w '%{http_code}' -u "admin:$token" "$cloud/graph/v1.0/me")" = 401 ] && login
    sleep 2
  done
  echo "$id"
}

login
alice=$(id_of alice)
bob=$(id_of bob)

# the wait may have used up the first token
login
application=$(api "$cloud/graph/v1.0/applications" | grep -o '"displayName":"OpenCloud","id":"[^"]*"' | cut -d '"' -f 8)

# a user has one role, the assignment replaces User
if ! api "$cloud/graph/v1.0/users/$alice/appRoleAssignments" | grep -q "$space_admin"; then
  api -o /dev/null -H 'Content-Type: application/json' "$cloud/graph/v1.0/users/$alice/appRoleAssignments" \
    --data "{\"principalId\":\"$alice\",\"appRoleId\":\"$space_admin\",\"resourceId\":\"$application\"}"
  echo "seed: alice is a space admin"
fi

if ! api "$cloud/graph/v1.0/groups?\$search=Support" | grep -q '"displayName":"Support"'; then
  group=$(api -H 'Content-Type: application/json' "$cloud/graph/v1.0/groups" --data '{"displayName":"Support"}' | field id)
  api -o /dev/null -H 'Content-Type: application/json' "$cloud/graph/v1.0/groups/$group/members/\$ref" \
    --data "{\"@odata.id\":\"$cloud/graph/v1.0/users/$bob\"}"
  echo "seed: Support is made, with bob"
fi

if api "$cloud/graph/v1.0/me/drives" | grep -q '"name":"Handbook"'; then
  echo "seed: Handbook is there already"
  exit 0
fi

drive=$(api -H 'Content-Type: application/json' "$cloud/graph/v1.0/drives" \
  --data '{"name":"Handbook","driveType":"project","description":"How we work"}' | field id)

invite() {
  api -o /dev/null -H 'Content-Type: application/json' "$cloud/graph/v1beta1/drives/$drive/root/invite" \
    --data "{\"recipients\":[{\"objectId\":\"$1\",\"@libre.graph.recipient.type\":\"user\"}],\"roles\":[\"$2\"]}"
}

invite "$alice" "$manager"
invite "$bob" "$viewer"

api -o /dev/null -X MKCOL "$cloud/dav/spaces/$drive/guides"
# a PUT from stdin goes chunked, which OpenCloud refuses; --data-binary sends its length
printf '# Onboarding\n\n## First day\n\nAsk for your keys at the front desk.\n' |
  api -o /dev/null -X PUT -H 'Content-Type: text/markdown' --data-binary @- "$cloud/dav/spaces/$drive/guides/onboarding.md"

echo "seed: Handbook is made"
