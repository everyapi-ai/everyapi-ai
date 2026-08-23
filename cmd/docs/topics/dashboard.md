# The web dashboard

`https://app.everyapi.ai` — sign-up, keys, spend, and everything that genuinely needs a browser: card payment, passkey registration, email verification, OAuth binding.

## What lives where

```
/keys           API keys: create, restrict, revoke. Shown once
/playground     Chat against any model, billed to your wallet
/create         Image, video, music and speech, with a history
/models         The model catalog
/model-pricing  Per-model rate sheet
/logs           Request log, filterable by key, model, group, time
/data           Usage charts
/performance    Per-model success rate, latency, throughput
/wallet         Balance and payment methods
/recharge       Top-up
/orders         Payment history
/redeem         Apply a redemption key
/subscriptions  Plans and your active subscription
/pricing        Plan comparison
/checkin        Daily check-in calendar
/referral       Affiliate code and referral rewards
/profile        Identity, 2FA, passkeys, OAuth bindings
/settings       Account preferences
/messages       Direct messages
/announcements  Platform announcements
/channels       Seller channels you have mounted
/seller         Seller earnings and sales
/sessions       Agent sessions
/artifacts      HTML reports published with `artifacts share`
/connect        EveryAPI Connect downloads and setup
/device         Approve a CLI device-code sign-in
/admin          Operator console (admin accounts only)
```

## Things the browser is required for

The CLI covers most of the account surface, but four flows are browser-only by design:

- **Card payment.** Collection happens out of band with the payment processor. `everyapi wallet topup` hands off with an anti-phishing phrase you must verify — see the `billing` topic.
- **Passkey registration.** WebAuthn needs a browser.
- **Email and social verification.**
- **Buying a subscription plan.**

2FA is not on that list: `everyapi account 2fa enable` enrolls from the terminal.

## Approving a CLI sign-in

`everyapi auth login` prints a code and a QR. The QR points at `/device` with the code already in the query string, so on a phone already signed in the flow is: scan, glance at the code, press Approve. That is the whole point of the device flow — no password is typed on the machine being authorised.

## Sign-in methods

Email and password, GitHub, Discord, LinuxDO, Telegram, generic OIDC, and passkeys. A self-hosted deployment enables whichever subset its operator configured.

## Self-hosted deployments

The standard backend image serves the API only and returns a status page at `/`. To get the dashboard on the same origin, build the embedded image — `deploy/Dockerfile.embed`. See the `self-hosting` topic.
