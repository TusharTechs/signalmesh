# ADR-005: Why semantic validation

Status: Accepted

HTTP 200 does not imply AI success. Explicit response contracts let
SignalMesh detect malformed, incomplete, truncated, or low-confidence
outputs and treat them as semantic failures.