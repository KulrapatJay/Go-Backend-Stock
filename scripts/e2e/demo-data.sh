#!/usr/bin/env bash
# Puts a demo account with two portfolios and a few trades into a running stack, through the API like
# a user would, so the data passes the same validation and audit as real data.
#
#   email     demo-investor@example.com
#   password  Zq9!kVx1   (a placeholder for local demos; never use it anywhere real)
#
# Environment as in flow.sh (API_ORIGIN, MAILPIT_URL). Run it again and it only says the account is
# there. The prices of the demo price provider are sample data, labelled is_demo in the API.
# Symbols the demo provider knows: AAPL, MSFT, NVDA, GOOGL, AMZN, META, TSLA, SPY.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

EMAIL=demo-investor@example.com
JAR="$WORK/demo.jar"

step "look for the demo account"
login "$JAR" "$EMAIL" "$PASSWORD"
if [[ $STATUS == 200 ]]; then
  echo "The demo account $EMAIL already exists; nothing to do."
  exit 0
fi
[[ $STATUS == 401 ]] || fail "login answered $STATUS, want 200 or 401"

step "create the demo account"
register "$JAR" "$EMAIL"
confirm_email "$JAR" "$EMAIL" "$CHALLENGE"
login "$JAR" "$EMAIL" "$PASSWORD"
expect_status 200

trade() { # PORTFOLIO_ID SYMBOL SIDE QUANTITY PRICE FEE EXECUTED_AT
  call "$JAR" POST "/portfolios/$1/transactions" \
    "{\"symbol\":\"$2\",\"side\":\"$3\",\"quantity\":\"$4\",\"price\":\"$5\",\"fee\":\"$6\",\"executed_at\":\"$7\"}" \
    -H "Idempotency-Key: demo-$1-$2-$3-$7"
  expect_status 201
}

step "long-term portfolio"
call "$JAR" POST /portfolios '{"name":"ระยะยาว","description":"ถือยาว ทบทวนปีละครั้ง"}'
expect_status 201
LONG=$(jq -r .id "$WORK/body")
trade "$LONG" MSFT buy 2 380.5 1 2026-08-03T14:30:00Z
trade "$LONG" NVDA buy 5 110.25 1 2026-08-17T14:30:00Z
trade "$LONG" SPY buy 3 480 0 2026-09-01T14:30:00Z

step "speculative portfolio"
call "$JAR" POST /portfolios '{"name":"เก็งกำไร","description":"ซื้อขายระยะสั้น"}'
expect_status 201
SPEC=$(jq -r .id "$WORK/body")
trade "$SPEC" TSLA buy 4 240 1 2026-09-14T14:30:00Z
trade "$SPEC" TSLA sell 2 265 1 2026-09-21T14:30:00Z
trade "$SPEC" AAPL buy 10 180.5 0 2026-10-05T14:30:00Z

for id in "$LONG" "$SPEC"; do
  call "$JAR" GET "/portfolios/$id/valuation"
  expect_status 200
  echo "$(jq -r .portfolio_id "$WORK/body") coverage=$(jq -r .coverage "$WORK/body") market_value=$(jq -r .market_value "$WORK/body") unrealized_pnl=$(jq -r .unrealized_pnl "$WORK/body") realized_pnl=$(jq -r .realized_pnl "$WORK/body")"
done
echo "Demo account ready: $EMAIL (prices are demo sample data)."
