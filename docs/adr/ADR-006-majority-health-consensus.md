# ADR-006: Why majority health consensus

Status: Accepted

A lightweight majority quorum over recent observations is sufficient for
the prototype and easy to explain. This is NOT Raft and does not provide
formal consensus safety. Under partition, nodes fall back to local
observations. Trade-off documented honestly.