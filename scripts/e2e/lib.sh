#!/usr/bin/env bash
# Helpers shared by the scripts in this directory: they talk to a running API like a browser does.
# Source this file; it does not run anything. It reads API_ORIGIN (default http://localhost:8080,
# which must equal the API's PUBLIC_ORIGIN) and MAILPIT_URL (default http://localhost:8025).
# Nothing here prints cookies, tokens or one-time codes.
set -euo pipefail

API_ORIGIN=${API_ORIGIN:-http://localhost:8080}
MAILPIT_URL=${MAILPIT_URL:-http://localhost:8025}
API="$API_ORIGIN/api/v1"
PASSWORD='Zq9!kVx1'
NEW_PASSWORD='Hn4#pLw8'

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

STEP=start
STATUS=''

step() {
  STEP=$1
  echo "- $1"
}

fail() {
  local rid
  rid=$(jq -r '.error.request_id // empty' "$WORK/body" 2>/dev/null || true)
  echo "FAIL [$STEP] $*${rid:+ (request_id=$rid)}" >&2
  exit 1
}

csrf() {
  curl -s -S -b "$1" -c "$1" "$API/auth/csrf" | jq -r .csrf_token
}

# call JAR METHOD PATH [JSON_BODY [CURL_ARG...]]
# Sends the request with the Origin and CSRF token a browser would send for a change, and leaves the
# status in STATUS, the body in $WORK/body and the headers in $WORK/headers.
call() {
  local jar=$1 method=$2 path=$3 body=${4:-}
  local extra=()
  if [[ $# -gt 4 ]]; then extra=("${@:5}"); fi
  local args=(-s -S -b "$jar" -c "$jar" -D "$WORK/headers" -o "$WORK/body" -w '%{http_code}' -X "$method")
  if [[ $method != GET ]]; then
    args+=(-H "Origin: $API_ORIGIN" -H "X-CSRF-Token: $(csrf "$jar")")
  fi
  if [[ -n $body ]]; then
    args+=(-H 'Content-Type: application/json' -d "$body")
  fi
  STATUS=$(curl "${args[@]}" ${extra[@]+"${extra[@]}"} "$API$path") || fail "curl could not reach $API$path"
}

expect_status() {
  [[ $STATUS == "$1" ]] || fail "status $STATUS, want $1 (error code: $(jq -r '.error.code // "none"' "$WORK/body" 2>/dev/null || echo unreadable))"
}

expect_error() {
  expect_status "$1"
  local got
  got=$(jq -r '.error.code // "none"' "$WORK/body")
  [[ $got == "$2" ]] || fail "error code $got, want $2"
}

expect_json() {
  local got
  got=$(jq -r "$1" "$WORK/body")
  [[ $got == "$2" ]] || fail "$1 = $got, want $2"
}

response_header() {
  grep -i "^$1:" "$WORK/headers" | head -1 | cut -d' ' -f2- | tr -d '\r'
}

# mail_otp EMAIL PHRASE: the 6-digit code that follows PHRASE in the newest message to EMAIL that
# has one. The mail is sent after the API has answered, so this polls for up to 30 seconds.
mail_otp() {
  local email=$1 phrase=$2 id otp
  for _ in $(seq 1 30); do
    for id in $(curl -s -S -G "$MAILPIT_URL/api/v1/search" --data-urlencode "query=to:$email" | jq -r '.messages[]?.ID'); do
      otp=$(curl -s -S "$MAILPIT_URL/api/v1/message/$id" | jq -r --arg p "$phrase" '.Text | capture($p + " (?<otp>[0-9]{6})") | .otp' 2>/dev/null || true)
      if [[ -n $otp ]]; then
        echo "$otp"
        return 0
      fi
    done
    sleep 1
  done
  return 1
}

register() { # JAR EMAIL -> CHALLENGE (global)
  call "$1" POST /auth/register "{\"email\":\"$2\",\"password\":\"$PASSWORD\"}"
  expect_status 202
  CHALLENGE=$(jq -r .challenge_id "$WORK/body")
}

confirm_email() { # JAR EMAIL CHALLENGE
  local otp
  otp=$(mail_otp "$2" 'รหัสยืนยันของคุณคือ') || fail "no verification code reached Mailpit for $2"
  call "$1" POST /auth/email-verification/confirm "{\"challenge_id\":\"$3\",\"otp\":\"$otp\"}"
  expect_status 204
}

login() { # JAR EMAIL PASSWORD
  call "$1" POST /auth/login "{\"email\":\"$2\",\"password\":\"$3\"}"
}

new_user() { # JAR EMAIL: signed up, verified and logged in
  register "$1" "$2"
  confirm_email "$1" "$2" "$CHALLENGE"
  login "$1" "$2" "$PASSWORD"
  expect_status 200
}

# A new address every call, even from flows started at the same second: $RANDOM repeats inside $(...)
# in some bash versions, and one address cannot ask for a second code within 60 seconds.
random_email() {
  echo "e2e-$(date +%s)-$$-$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')@example.com"
}
