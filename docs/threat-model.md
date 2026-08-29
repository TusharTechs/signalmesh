# Threat model (prototype)

- Secrets: provider keys via env vars only; never logged.
- Debug/chaos endpoints: unauthenticated by design for local development;
  must never be exposed publicly.
- SSRF: no arbitrary URL fetching from user input.
- Input validation: JSON decode errors rejected; bounded retries;
  enforced timeouts.
- Logging: structured; request content not persisted in prototype logs.