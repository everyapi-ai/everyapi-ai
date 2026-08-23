# The HTTP API

EveryAPI speaks three request dialects on one host: OpenAI (Chat Completions and Responses), Anthropic Messages, and Google Gemini. Pick whichever your SDK already emits — the gateway converts to the upstream provider's format and converts the response back.

## Base URL and auth

```
Base URL   https://api.everyapi.ai          global
           https://api-cn.everyapi.ai       China-accelerated
Auth       Authorization: Bearer sk-everyapi-…
```

The Anthropic and Gemini surfaces also accept their native auth headers — `x-api-key` plus `anthropic-version` for Anthropic, `x-goog-api-key` or a `?key=` query parameter for Gemini — carrying the same `sk-everyapi-…` value. That is what lets an unmodified vendor SDK point at the gateway.

## OpenAI-compatible endpoints

```
POST   /v1/chat/completions      Chat Completions
POST   /v1/completions           Legacy completions
POST   /v1/responses             Responses API
POST   /v1/responses/compact     Responses compaction
DELETE /v1/responses/{id}        Delete a stored response
POST   /v1/embeddings            Embeddings
POST   /v1/rerank                Rerank
POST   /v1/moderations           Moderation
POST   /v1/images/generations    Image generation
POST   /v1/images/edits          Image editing
POST   /v1/audio/speech          Text to speech
POST   /v1/audio/transcriptions  Speech to text
POST   /v1/audio/translations    Speech translation
GET    /v1/realtime              Realtime (WebSocket)
GET    /v1/models                Model catalog
GET    /v1/models/{model}        One model
```

## Anthropic-compatible endpoints

```
POST   /v1/messages               Messages
POST   /v1/messages/count_tokens  Token counting (relayed, not billed)
```

`/v1/models` returns the Anthropic-shaped catalog when the request carries both `x-api-key` and `anthropic-version`.

## Gemini-compatible endpoints

```
POST   /v1beta/models/{model}:{action}   Native generate / stream / embed
GET    /v1beta/models                    Gemini-shaped catalog
GET    /v1beta/openai/models             OpenAI-shaped catalog, Gemini path
```

## Async media: submit then poll

Video and music are long-running, so they are submit-and-poll task channels rather than a single blocking call.

```
POST   /v1/video/generations            Submit (OpenAI-compatible)
GET    /v1/video/generations/{task_id}  Poll
POST   /v1/videos                       Submit (Sora-style)
GET    /v1/videos/{task_id}             Poll
POST   /v1/videos/{video_id}/remix      Remix an existing result
GET    /v1/videos/{task_id}/content     Fetch the finished media
DELETE /v1/videos/{task_id}             Cancel
```

Native provider surfaces exist alongside the compatible one, for clients that already speak them:

```
POST /kling/v1/videos/text2video            Kling, native shape
POST /kling/v1/videos/image2video
GET  /kling/v1/videos/text2video/{task_id}
GET  /kling/v1/videos/image2video/{task_id}
POST /jimeng/                Jimeng, official Action-parameter shape
POST /suno/submit/{action}   Suno music and lyrics
POST /suno/fetch             Suno batch poll
GET  /suno/fetch/{id}
POST /mj/submit/{action}     Midjourney (imagine, blend, describe, …)
GET  /mj/task/{id}/fetch     Midjourney poll
```

Supported async video providers include Sora, Kling, Hailuo, Jimeng, Vidu, and Doubao; Suno covers music.

## Streaming

Set `"stream": true` and read standard SSE: `data: <json>` frames terminated by `data: [DONE]`. The Anthropic and Gemini surfaces stream in their own native event shapes. Realtime uses a WebSocket at `/v1/realtime`.

## Endpoints that return 501

These exist in the OpenAI spec but are deliberately not proxied. They answer HTTP 501 with `code=endpoint_not_supported`:

```
POST   /v1/images/variations
GET    /v1/files          POST /v1/files
GET    /v1/files/{id}     DELETE /v1/files/{id}
GET    /v1/files/{id}/content
GET    /v1/fine-tunes     POST /v1/fine-tunes
GET    /v1/fine-tunes/{id}
POST   /v1/fine-tunes/{id}/cancel
GET    /v1/fine-tunes/{id}/events
DELETE /v1/models/{model}
```

Provider file storage and fine-tune lifecycles are too provider-specific to normalise at a gateway, and model deletion would conflict with the marketplace's channel ownership. For these, call the upstream provider directly with that provider's own credentials.

## Error responses and language

Errors come back in the OpenAI error envelope. Backend messages are translated: send `Accept-Language` and you get English, Simplified Chinese, Traditional Chinese, Japanese, Korean, Spanish, German, or French. The CLI sets this automatically from your language setting.

## Rate limits and quota

Per-user, per-model rate limits apply on top of the per-key quota. A key can additionally be restricted to a model list, a routing group, and an IP allowlist — see the `tokens` topic. Requests that exceed a limit return 429; requests with no quota left fail before reaching an upstream.
