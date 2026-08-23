# Money: quota, wallet, subscriptions

Every request is settled after the response, from the token usage the upstream actually reported. What it draws from — wallet balance or a subscription — is a preference you set.

## Checking where you stand

```
everyapi auth status        Identity, quota remaining and used, requests
everyapi stats usage        Day-by-day or per-model consumption
everyapi stats log summary  Per-model spend over the last 7 days
everyapi token usage <sk-…> One key's remaining quota — works signed out
```

Balances and prices are denominated in gateway quota units. `everyapi models pricing` prints the rate sheet in the same units, so the two are directly comparable.

## Topping up

```
everyapi wallet topup               Open the dashboard top-up page
everyapi wallet topup --no-browser  Print the URL only
everyapi wallet history [--limit N] [--page P] [--keyword K]
everyapi wallet info                Methods, minimum, suggested amounts
everyapi wallet redeem <key>        Apply a top-up / redemption key
```

Card collection happens in the browser, so `topup` is a handoff — but it is a verified one. Before opening anything the CLI asks the backend for a jump session and gets back a four-emoji phrase, for example `🌊 🦊 🍕 🚀`. It prints the phrase and the URL, and tells you the same phrase should appear at the top of the page. Compare them:

- Phrases match → you are on the real EveryAPI dashboard.
- Phrase missing or different → close the tab. Treat it as phishing.

The phrase lives in backend memory keyed by a random 32-hex session id, expires in ten minutes, and is deleted after the dashboard reads it once. A phishing site has no authenticated path to fetch it, and cannot read the phrase for a session id it did not create.

Payments are processed through Fluxa.

## Daily check-in

```
everyapi checkin          Claim today's reward
everyapi checkin claim    Same, explicit verb
everyapi checkin status [--month YYYY-MM]
everyapi checkin makeup <YYYY-MM-DD>
                          Cover a missed day: keeps the streak, no reward
```

## Subscriptions

```
everyapi account plans   List enabled plans
everyapi account self    Your active and past subscriptions
everyapi account subscription preference --set <value>
```

Preference values: `subscription_first`, `wallet_first`, `subscription_only`, `wallet_only`. It decides which source a request draws from when both are available. Buying a plan is a browser flow — use `everyapi wallet topup` to reach the dashboard behind the anti-phishing handshake.

## How a request is priced

Pricing is per model and can be per-call, per-token, or expression-driven for tiered rates, with separate cache pricing where the upstream distinguishes cached from fresh input tokens. Settlement happens after the response using the usage the upstream reported, not an estimate made before the call — which is why a streamed response's cost appears once the stream ends.

Unbilled exceptions exist and are deliberate: `/v1/messages/count_tokens` is relayed to the upstream token counter without being charged.

Every settled request writes a log row:

```
everyapi stats log list --model gpt-4o --since 24h
everyapi stats log stat --since 7d
```

## Earning

Three separate balances can pay into your main one:

```
everyapi account aff                    Your affiliate code
everyapi account aff reset              Rotate it
everyapi account aff transfer <amount>  Affiliate rewards to main balance
everyapi seller withdraw                All pending seller earnings
everyapi seller withdraw --quota 1000   Partial, in quota units
```

Referral rewards pay in two stages and are clustered by /24 subnet to limit self-referral abuse. Seller earnings accrue per buyer charge on channels you mounted — see the `seller` topic.

## Quota warnings

```
everyapi account setting               Show quota-warning settings
everyapi account setting --type <ch>   Channels the warnings go to
everyapi account setting test          Send a test notification
```
