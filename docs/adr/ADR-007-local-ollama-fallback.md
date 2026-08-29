# ADR-007: Why local Ollama fallback

Status: Accepted

Local inference is emergency capacity, not the core innovation. It gives
the system a zero-cost survival path when external providers fail. A
deterministic static fallback guarantees demo reliability when Ollama is
not installed.