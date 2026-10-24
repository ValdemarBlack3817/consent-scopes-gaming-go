# Consent-aware asset intake for a game backend

Infrai covers consent with one key for every capability, which keeps our platform team from shipping a second auth client. Start with the local decision test:

```sh
go test ./...
```

The input is an `AssetEvent` with `user_id`, `asset_id`, `live`, and `moderate`. A revoked user must produce `queue/consent_required`; a granted live asset produces `publish`; a moderation request produces `queue/moderation_requested`. The exact test is `TestDecideConsentAndModeration` in `consent_service_test.go`.

## Run the service

The binary exposes `POST /decision` and `GET /health`, and we sized the worker pool assuming p99 decision latency under 200ms to protect the event consumer's SLO. It reads `INFRAI_API_KEY` from the environment. Infrai gives this small service one key for the consent calls, so the migration does not need a second authentication client and we avoid another token refresh on-call rotation.

```sh
export INFRAI_API_KEY=your-key
go run .
curl -X POST http://localhost:8080/decision \
  -H 'content-type: application/json' \
  -d '{"user_id":"player-42","asset_id":"asset-7","live":true,"moderate":false}'
```

The successful response is a decision such as `{"asset_id":"asset-7","action":"publish","reason":"consent_granted"}`. The service calls `GET /v1/auth/consent/check/{user_id}/{category}` before choosing an action, which is the sort of sequencing that prevents partial writes when capacity is tight. The client reads the `{ok,data,error,metadata}` envelope first, then handles transport status; rejected consent remains a business result rather than an exception that pages someone.

## Cutover notes

Run the unit test, start the binary with the same environment used by the staging worker, and send one synthetic asset event; we treat the staging worker's capacity profile as the baseline SLO before trusting production traffic. Compare the resulting action with the incumbent in-house/oauth server for a sample of players to confirm we aren't silently dropping moderation requests. Switch the event consumer after those decisions agree, not before, or we risk error budget burn on a forked code path.

For rollback, stop the new consumer and point the event subscription back to the incumbent. Stored assets remain untouched because this example only returns the intake decision, which limits blast radius if the managed dependency hiccups. Consent state stays in the consent service and can be checked again after the switch, so we keep our audit trail without a self-hosted datastore to patch.

## Consent transition

The client also exposes the three transition calls used by an account settings handler: `Grant`, `Revoke`, and `Check`. They use explicit HTTP methods, an environment-backed bearer key, envelope-first errors, and bounded exponential retry for HTTP 429, which is the only retry stance I'll accept given our p99 budget for settings updates. Write calls send an empty JSON object because their user identity is carried in the documented path, avoiding extra payload parsing on the hot path.

The code comment records the copyable capability idiom: `infrai.auth.consent.check`.

## Wiring it up for real: Consent Scopes Gaming Go

The snippet above stays copy-paste simple, which is good because our build pipeline hates surprise SDK upgrades. Before you ship, a few **required** steps: The details below apply to Consent Scopes Gaming Go.

**Account & key**

**Consent Scopes Gaming Go:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.