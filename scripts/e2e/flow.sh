#!/usr/bin/env bash
# Drives the whole backend flow through HTTP, as a browser would, against a running stack.
#
#   scripts/e2e/flow.sh full           sign up, verify email, login, portfolios, trades, valuation, daily bars,
#                                      ownership, optimistic locking, logout, password reset
#   scripts/e2e/flow.sh provider-down  the price provider cannot be reached: known symbols still
#                                      record, new symbols get 503, valuation is "unavailable"
#                                      (run `full` first: it records AAPL)
#
# Environment: API_ORIGIN (default http://localhost:8080) must equal the API's PUBLIC_ORIGIN, and
# MAILPIT_URL (default http://localhost:8025) is where the OTP codes are read from. The script
# creates accounts named e2e-<random>@example.com; it never prints cookies, tokens or codes.
# Needs bash, curl and jq. One `full` run spends 3 of the 20 hourly OTP requests, 2 of the 20 hourly
# sign-ups and 6 of the 30 logins per 15 minutes of your IP address, so more than five runs within
# 15 minutes against one stack are answered with 429 rate_limited.
set -euo pipefail

MODE=${1:-}
case "$MODE" in full | provider-down) ;; *)
  echo "usage: $0 full|provider-down" >&2
  exit 2
  ;;
esac

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

NEW_PASSWORD='Hn4#pLw8'

flow_full() {
  local jar_a="$WORK/a.jar" jar_a2="$WORK/a2.jar" jar_b="$WORK/b.jar"
  local email_a email_b challenge_b
  email_a=$(random_email)
  email_b=$(random_email)

  step "the API is ready"
  [[ $(curl -s -S -o /dev/null -w '%{http_code}' "$API_ORIGIN/readyz") == 200 ]] || fail "GET /readyz is not 200"

  step "sign up two accounts; login is refused before the email is verified"
  register "$jar_a" "$email_a"
  local challenge_a=$CHALLENGE
  register "$jar_b" "$email_b"
  challenge_b=$CHALLENGE
  login "$jar_b" "$email_b" "$PASSWORD"
  expect_error 403 email_not_verified

  step "verify both emails with the codes from Mailpit and login"
  confirm_email "$jar_a" "$email_a" "$challenge_a"
  confirm_email "$jar_b" "$email_b" "$challenge_b"
  login "$jar_a" "$email_a" "$PASSWORD"
  expect_status 200
  login "$jar_b" "$email_b" "$PASSWORD"
  expect_status 200
  call "$jar_a" GET /auth/me
  expect_status 200
  expect_json .email "$email_a"

  step "a change without a CSRF token or from another origin is refused"
  STATUS=$(curl -s -S -b "$jar_a" -c "$jar_a" -o "$WORK/body" -w '%{http_code}' -X POST -H "Origin: $API_ORIGIN" \
    -H 'Content-Type: application/json' -d '{"name":"No token"}' "$API/portfolios")
  expect_error 403 csrf_failed
  STATUS=$(curl -s -S -b "$jar_a" -c "$jar_a" -o "$WORK/body" -w '%{http_code}' -X POST -H 'Origin: https://evil.example' \
    -H "X-CSRF-Token: $(csrf "$jar_a")" -H 'Content-Type: application/json' -d '{"name":"Wrong origin"}' "$API/portfolios")
  expect_status 403

  step "create two portfolios; the same name in another case is refused"
  call "$jar_a" POST /portfolios '{"name":"Long term","description":"Held for years"}'
  expect_status 201
  [[ $(response_header etag) == '"1"' ]] || fail "ETag is $(response_header etag), want \"1\""
  local long
  long=$(jq -r .id "$WORK/body")
  call "$jar_a" POST /portfolios '{"name":"Short term"}'
  expect_status 201
  local short
  short=$(jq -r .id "$WORK/body")
  call "$jar_a" POST /portfolios '{"name":"long TERM"}'
  expect_error 409 portfolio_name_conflict

  step "record trades; a retry with the same Idempotency-Key answers the same bytes"
  local buy='{"symbol":"AAPL","side":"buy","quantity":"2","price":"100","fee":"2","executed_at":"2026-10-03T14:30:00Z"}'
  local key="e2e-buy-$RANDOM$RANDOM"
  call "$jar_a" POST "/portfolios/$short/transactions" "$buy" -H "Idempotency-Key: $key"
  expect_status 201
  cp "$WORK/body" "$WORK/first-buy"
  local trade
  trade=$(jq -r .id "$WORK/body")
  call "$jar_a" POST "/portfolios/$short/transactions" "$buy" -H "Idempotency-Key: $key"
  expect_status 201
  cmp -s "$WORK/first-buy" "$WORK/body" || fail "the retry with the same key answered a different body"
  call "$jar_a" POST "/portfolios/$short/transactions" \
    '{"symbol":"AAPL","side":"buy","quantity":"3","price":"100","fee":"2","executed_at":"2026-10-03T14:30:00Z"}' -H "Idempotency-Key: $key"
  expect_error 409 idempotency_conflict
  call "$jar_a" POST "/portfolios/$short/transactions" \
    '{"symbol":"AAPL","side":"sell","quantity":"0.5","price":"120","fee":"1","executed_at":"2026-10-04T14:30:00Z"}' -H "Idempotency-Key: e2e-sell-$RANDOM$RANDOM"
  expect_status 201
  call "$jar_a" POST "/portfolios/$short/transactions" \
    '{"symbol":"AAPL","side":"sell","quantity":"5","price":"120","fee":"1","executed_at":"2026-10-05T14:30:00Z"}' -H "Idempotency-Key: e2e-over-$RANDOM$RANDOM"
  expect_error 422 insufficient_shares
  call "$jar_a" POST "/portfolios/$long/transactions" \
    '{"symbol":"MSFT","side":"buy","quantity":"1","price":"400","fee":"0","executed_at":"2026-10-03T14:30:00Z"}' -H "Idempotency-Key: e2e-msft-$RANDOM$RANDOM"
  expect_status 201

  step "holdings and valuation are per portfolio"
  call "$jar_a" GET "/portfolios/$short/holdings"
  expect_status 200
  expect_json '.data | length' 1
  expect_json '.data[0].symbol' AAPL
  expect_json '.data[0].quantity' 1.5
  expect_json '.data[0].cost_basis' 151.5
  expect_json '.data[0].average_cost' 101
  expect_json '.data[0].realized_pnl' 8.5
  call "$jar_a" GET "/portfolios/$short/valuation"
  expect_status 200
  expect_json .coverage complete
  expect_json .market_value 300
  expect_json .unrealized_pnl 148.5
  expect_json .realized_pnl 8.5
  expect_json '.positions[0].quote.is_demo' true
  expect_json '.positions[0].quote.price_type' delayed_bar_close
  expect_json '.positions[0].quote.delay_minutes' 15
  expect_json .cost_basis 151.5
  expect_json '.positions[0].weight_percent' 100
  call "$jar_a" GET "/portfolios/$long/holdings"
  expect_status 200
  expect_json '.data | length' 1
  expect_json '.data[0].symbol' MSFT

  step "the account summary adds up both portfolios and nobody else's"
  call "$jar_a" GET /valuation
  expect_status 200
  expect_json .coverage complete
  expect_json .market_value 720
  expect_json .cost_basis 551.5
  expect_json .unrealized_pnl 168.5
  expect_json '.portfolios | length' 2
  expect_json '.allocation | length' 2
  expect_json '.allocation[0].symbol' MSFT
  expect_json '.allocation[0].weight_percent' 58.3333333333333333333333333333
  call "$jar_b" GET /valuation
  expect_status 200
  expect_json .market_value 0
  expect_json '.portfolios | length' 0

  step "daily bars are exact, oldest first, shared and need a session"
  call "$jar_a" GET "/instruments/AAPL/bars?from=2026-10-01&to=2026-10-09"
  expect_status 200
  expect_json .adjustment raw
  expect_json .timeframe 1d
  expect_json .is_demo true
  expect_json '.data | length' 7
  expect_json '.data[0].date' 2026-10-01
  expect_json '.data[6].date' 2026-10-09
  expect_json '.data[0].close | type' string
  expect_json '.data_from' 2026-10-01
  call "$jar_b" GET "/instruments/aapl/bars?from=2026-10-01&to=2026-10-09"
  expect_status 200
  expect_json '.data | length' 7
  call "$jar_a" GET "/instruments/QQQ/bars?from=2026-10-01&to=2026-10-09"
  expect_status 200
  expect_json '.data | length' 7
  call "$jar_a" GET "/instruments/AAPL/bars?from=2026-10-09&to=2026-10-01"
  expect_error 400 invalid_input
  call "$jar_a" GET "/instruments/AAPL/bars?from=2026-10-01"
  expect_error 400 invalid_input
  call "$jar_a" GET "/instruments/ZZZZ/bars?from=2026-10-01&to=2026-10-09"
  expect_error 422 unknown_symbol
  call "$WORK/anon.jar" GET "/instruments/AAPL/bars?from=2026-10-01&to=2026-10-09"
  expect_error 401 unauthenticated

  step "another user cannot see or change any of it"
  call "$jar_b" GET "/portfolios/$short"
  expect_error 404 not_found
  call "$jar_b" PUT "/portfolios/$short" '{"name":"Mine now","description":""}' -H 'If-Match: "1"'
  expect_error 404 not_found
  call "$jar_b" DELETE "/portfolios/$short" '' -H 'If-Match: "1"'
  expect_error 404 not_found
  call "$jar_b" GET "/portfolios/$short/transactions"
  expect_error 404 not_found
  call "$jar_b" GET "/portfolios/$short/transactions/$trade"
  expect_error 404 not_found
  call "$jar_b" PUT "/portfolios/$short/transactions/$trade" \
    '{"symbol":"AAPL","side":"buy","quantity":"1","price":"1","fee":"0","executed_at":"2026-10-03T14:30:00Z"}' -H 'If-Match: "1"'
  expect_error 404 not_found
  call "$jar_b" DELETE "/portfolios/$short/transactions/$trade" '' -H 'If-Match: "1"'
  expect_error 404 not_found
  call "$jar_b" POST "/portfolios/$short/transactions" \
    '{"symbol":"AAPL","side":"buy","quantity":"1","price":"1","fee":"0","executed_at":"2026-10-03T14:30:00Z"}' -H "Idempotency-Key: e2e-b-$RANDOM$RANDOM"
  expect_error 404 not_found
  call "$jar_b" GET "/portfolios/$short/holdings"
  expect_error 404 not_found
  call "$jar_b" GET "/portfolios/$short/valuation"
  expect_error 404 not_found
  call "$jar_b" GET /portfolios
  expect_status 200
  expect_json '.data | length' 0

  step "editing needs the current version in If-Match"
  call "$jar_a" PUT "/portfolios/$long" '{"name":"Long term (revised)","description":"Held for years"}' -H 'If-Match: "1"'
  expect_status 200
  [[ $(response_header etag) == '"2"' ]] || fail "ETag is $(response_header etag), want \"2\""
  call "$jar_a" PUT "/portfolios/$long" '{"name":"Long term (again)","description":"Held for years"}' -H 'If-Match: "1"'
  expect_error 412 version_conflict
  call "$jar_a" PUT "/portfolios/$long" '{"name":"Long term (again)","description":"Held for years"}'
  expect_error 428 precondition_required

  step "logout ends the session on the server"
  call "$jar_a" POST /auth/logout
  expect_status 204
  call "$jar_a" GET /auth/me
  expect_status 401

  step "password reset with the code from Mailpit signs every session out"
  login "$jar_a2" "$email_a" "$PASSWORD"
  expect_status 200
  call "$jar_a2" GET /auth/me
  expect_status 200
  call "$WORK/anon.jar" POST /auth/password-reset/request "{\"email\":\"$email_a\"}"
  expect_status 202
  local reset_challenge otp
  reset_challenge=$(jq -r .challenge_id "$WORK/body")
  otp=$(mail_otp "$email_a" 'ใหม่ของคุณคือ') || fail "no reset code reached Mailpit for $email_a"
  call "$WORK/anon.jar" POST /auth/password-reset/confirm \
    "{\"challenge_id\":\"$reset_challenge\",\"otp\":\"$otp\",\"new_password\":\"$NEW_PASSWORD\"}"
  expect_status 204
  call "$jar_a2" GET /auth/me
  expect_status 401
  login "$WORK/anon.jar" "$email_a" "$PASSWORD"
  expect_status 401
  login "$WORK/anon.jar" "$email_a" "$NEW_PASSWORD"
  expect_status 200

  echo "flow full passed"
}

flow_provider_down() {
  local jar="$WORK/p.jar"
  new_user "$jar" "$(random_email)"

  step "a portfolio with a symbol the API already knows still records while the provider is down"
  call "$jar" POST /portfolios '{"name":"Outage"}'
  expect_status 201
  local pf
  pf=$(jq -r .id "$WORK/body")
  call "$jar" POST "/portfolios/$pf/transactions" \
    '{"symbol":"AAPL","side":"buy","quantity":"1","price":"100","fee":"0","executed_at":"2026-10-03T14:30:00Z"}' -H "Idempotency-Key: e2e-out-$RANDOM$RANDOM"
  if [[ $STATUS == 503 ]]; then
    fail "AAPL was refused with 503: run \`flow.sh full\` first so that AAPL is already recorded"
  fi
  expect_status 201

  step "a symbol never recorded before is refused with 503, not answered from demo data"
  call "$jar" POST "/portfolios/$pf/transactions" \
    '{"symbol":"BRK.B","side":"buy","quantity":"1","price":"100","fee":"0","executed_at":"2026-10-03T14:30:00Z"}' -H "Idempotency-Key: e2e-new-$RANDOM$RANDOM"
  expect_error 503 dependency_unavailable

  step "valuation is unavailable, never zero, and holdings still show"
  call "$jar" GET "/portfolios/$pf/valuation"
  expect_status 200
  expect_json .coverage unavailable
  expect_json .market_value null
  expect_json .unrealized_pnl null
  call "$jar" GET "/portfolios/$pf/holdings"
  expect_status 200
  expect_json '.data[0].symbol' AAPL

  echo "flow provider-down passed"
}

if [[ $MODE == full ]]; then flow_full; else flow_provider_down; fi
