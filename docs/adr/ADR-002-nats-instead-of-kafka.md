# ADR-002: Why NATS instead of Kafka

Status: Accepted

NATS is dramatically lighter to operate for a hackathon while still
providing pub/sub, clustering, and optional JetStream persistence.
Kafka would add operational complexity without materially improving the
demonstration.